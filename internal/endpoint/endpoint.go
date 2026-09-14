// Package endpoint is the policy for URLs an admin types into acquire: the base
// address of a download client, a search source, and the links acquire then
// fetches on their behalf.
//
// Configuration screens turn acquire into something that makes requests to
// whatever address it is given, from inside the cluster. The policy keeps that
// from reaching what nobody meant to expose:
//
//   - http and https only, no credentials in the URL (they belong in the
//     write-only secret fields, not in a column that is shown back);
//   - link-local and cloud metadata addresses are refused, both as literals and
//     at dial time, so DNS cannot be used to point a harmless-looking name at
//     them;
//   - ACQUIRE_ENDPOINT_DENY names hosts or domain suffixes that are refused;
//   - cluster service names in another namespace are refused unless
//     ACQUIRE_ENDPOINT_ALLOW_INTERNAL=true — acquire's own namespace, and
//     ordinary private (RFC 1918) addresses, stay allowed;
//   - responses are read with a size cap, and a redirect may not change scheme.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Policy decides which URLs are acceptable.
type Policy struct {
	// Deny holds hostnames or domain suffixes that are refused.
	Deny []string
	// AllowInternal permits cluster service names outside Namespace.
	AllowInternal bool
	// Namespace is acquire's own namespace; "" when not running in a cluster.
	Namespace string
	// Resolve looks a host up for CheckResolved; nil uses the default resolver.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// ErrTooLarge is returned by ReadCapped when a body exceeds its cap.
var ErrTooLarge = errors.New("response exceeds the size limit")

var (
	linkLocal4 = netip.MustParsePrefix("169.254.0.0/16")
	linkLocal6 = netip.MustParsePrefix("fe80::/10")
	metadata   = []netip.Addr{
		netip.MustParseAddr("100.100.100.200"),
		netip.MustParseAddr("fd00:ec2::254"),
	}
)

// Refused reports whether an address is one acquire never connects to:
// link-local ranges (which include the common metadata address) and the cloud
// metadata addresses that live outside them.
func Refused(a netip.Addr) bool {
	// A zoned address (fe80::1%eth0) is outside every prefix as far as
	// netip.Prefix.Contains is concerned; the zone only picks the interface.
	a = a.Unmap().WithZone("")
	if linkLocal4.Contains(a) || linkLocal6.Contains(a) {
		return true
	}
	for _, m := range metadata {
		if a == m {
			return true
		}
	}
	return false
}

// Check validates a URL without touching the network and returns it parsed.
func (p Policy) Check(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("an address is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("not a valid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, errors.New("only http and https addresses are allowed")
	}
	if u.User != nil {
		return nil, errors.New("put credentials in the username and secret fields, not in the address")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return nil, errors.New("the address has no host")
	}
	if a, err := netip.ParseAddr(host); err == nil && Refused(a) {
		return nil, fmt.Errorf("%s is a link-local or metadata address", host)
	}
	for _, d := range p.Deny {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return nil, fmt.Errorf("%s is refused by ACQUIRE_ENDPOINT_DENY", host)
		}
	}
	if ns, ok := serviceNamespace(host); ok && !p.AllowInternal && ns != p.Namespace {
		return nil, fmt.Errorf("%s is a service in namespace %q, outside acquire's own; set ACQUIRE_ENDPOINT_ALLOW_INTERNAL=true to allow it", host, ns)
	}
	return u, nil
}

// serviceNamespace extracts the namespace from a cluster service name:
// name.ns.svc or name.ns.svc.cluster.local.
func serviceNamespace(host string) (string, bool) {
	h := strings.TrimSuffix(host, ".cluster.local")
	if !strings.HasSuffix(h, ".svc") {
		return "", false
	}
	labels := strings.Split(strings.TrimSuffix(h, ".svc"), ".")
	return labels[len(labels)-1], true
}

// CheckResolved runs Check and then, best effort, resolves the host: a name
// that currently resolves to a refused address is rejected at save time rather
// than only failing later at dial time. A lookup failure is NOT an error — the
// service may simply not be deployed yet.
func (p Policy) CheckResolved(ctx context.Context, raw string) error {
	u, err := p.Check(raw)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	resolve := p.Resolve
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := resolve(ctx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if Refused(a) {
			return fmt.Errorf("%s resolves to a link-local or metadata address", host)
		}
	}
	return nil
}

// dialControl refuses a connection to a refused address after DNS resolution,
// so a name cannot smuggle the request there.
func dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if a, err := netip.ParseAddr(host); err == nil && Refused(a) {
		return fmt.Errorf("refusing to connect to %s: link-local or metadata address", a)
	}
	return nil
}

// ErrMagnetRedirect carries the magnet link a download URL redirected to.
// Torrent indexers commonly answer a .torrent link with a redirect to a magnet;
// that is not a failure, it is the source.
type ErrMagnetRedirect struct{ Magnet string }

func (e *ErrMagnetRedirect) Error() string { return "the link redirects to a magnet" }

// HTTPClient returns a client that applies the policy to every connection and
// redirect. It ignores proxy environment variables on purpose: through a proxy
// the dial-time check would see the proxy's address, not the target's.
func (p Policy) HTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: dialControl}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if strings.EqualFold(req.URL.Scheme, "magnet") {
				return &ErrMagnetRedirect{Magnet: req.URL.String()}
			}
			if !strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) {
				return errors.New("refusing a redirect to another scheme")
			}
			if _, err := p.Check(req.URL.String()); err != nil {
				return fmt.Errorf("refusing redirect: %w", err)
			}
			return nil
		},
	}
}

// ReadCapped reads at most max bytes and fails with ErrTooLarge beyond that,
// rather than silently truncating a file that would then be corrupt.
func ReadCapped(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

// credentialParams are query parameters that carry a search source's or a
// tracker's credential.
var credentialParams = map[string]bool{"apikey": true, "r": true, "passkey": true, "token": true}

// Redact strips credentials from a link so it can be stored, logged or shown:
// the apikey, r, passkey and token query parameters, and any password in the
// user info. Tracker URLs inside a magnet are redacted the same way. A link
// that does not parse is replaced entirely, since it cannot be shown safely.
func Redact(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "(link withheld)"
	}
	changed := false
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.User(u.User.Username())
		changed = true
	}
	q := u.Query()
	for k, vs := range q {
		if credentialParams[strings.ToLower(k)] {
			q.Del(k)
			changed = true
			continue
		}
		if strings.EqualFold(u.Scheme, "magnet") && strings.EqualFold(k, "tr") {
			for i, v := range vs {
				if r := Redact(v); r != v {
					vs[i] = r
					changed = true
				}
			}
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// RedactError replaces the URL inside a transport error with its redacted
// form. net/http puts the full request URL into every error it returns, which
// would otherwise carry a source's API key into a request's status line.
func RedactError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return &url.Error{Op: ue.Op, URL: Redact(ue.URL), Err: ue.Err}
	}
	return err
}
