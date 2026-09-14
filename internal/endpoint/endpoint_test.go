package endpoint

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestCheck(t *testing.T) {
	p := Policy{Deny: []string{"example.org", ".blocked.test", "exact-host"}, Namespace: "media"}
	cases := []struct {
		url string
		ok  bool
	}{
		{"http://worker:6789", true},
		{"https://worker.media.svc:8080/rpc", true},
		{"http://worker.media.svc.cluster.local", true},
		{"http://10.0.0.5:8080", true}, // RFC 1918 stays allowed
		{"http://192.168.1.20:8080", true},
		{"http://127.0.0.1:8080", true},
		{"ftp://worker", false},
		{"file:///etc/passwd", false},
		{"gopher://worker", false},
		{"http://", false},
		{"", false},
		{"http://user:pw@worker:8080", false}, // credentials belong in the secret field
		{"http://169.254.169.254/latest/meta-data", false},
		{"http://[fe80::1]/", false},
		{"http://[fe80::1%25eth0]:80/", false}, // a zone does not take it out of fe80::/10
		{"http://[fd00:ec2::254%25eth0]/", false},
		{"http://100.100.100.200/", false},
		{"http://[fd00:ec2::254]/", false},
		{"http://[::ffff:169.254.169.254]/", false},
		{"http://example.org", false},
		{"http://api.example.org", false},
		{"http://notexample.org", true}, // a suffix match is on a label boundary
		{"http://x.blocked.test", false},
		{"http://exact-host:80", false},
		{"http://EXACT-HOST.:80", false},
		{"http://worker.other.svc:8080", false},
		{"http://worker.other.svc.cluster.local", false},
	}
	for _, c := range cases {
		_, err := p.Check(c.url)
		if (err == nil) != c.ok {
			t.Errorf("Check(%q) err = %v, want ok=%v", c.url, err, c.ok)
		}
	}
}

func TestOtherNamespaceIsAllowedWhenInternalIsAllowed(t *testing.T) {
	p := Policy{Namespace: "media", AllowInternal: true}
	if _, err := p.Check("http://worker.other.svc.cluster.local"); err != nil {
		t.Fatal(err)
	}
	// Outside a cluster there is no own namespace: every service name is
	// "another" namespace and needs the explicit opt-in.
	if _, err := (Policy{}).Check("http://worker.media.svc"); err == nil {
		t.Fatal("a service name must be refused when acquire's namespace is unknown")
	}
}

func TestCheckResolvedRefusesNamesPointingAtMetadata(t *testing.T) {
	p := Policy{Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "sneaky":
			return []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("169.254.169.254")}, nil
		case "fine":
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}
		return nil, errors.New("no such host")
	}}
	if err := p.CheckResolved(context.Background(), "http://sneaky:80"); err == nil {
		t.Error("a name resolving to a metadata address was accepted")
	}
	if err := p.CheckResolved(context.Background(), "http://fine:80"); err != nil {
		t.Error(err)
	}
	// Not deployed yet is not a reason to refuse the configuration.
	if err := p.CheckResolved(context.Background(), "http://not-yet-deployed:80"); err != nil {
		t.Error(err)
	}
}

func TestDialControlRefusesLinkLocal(t *testing.T) {
	for _, addr := range []string{"169.254.169.254:80", "[fe80::1]:80", "[fe80::1%eth0]:80", "100.100.100.200:80", "[fd00:ec2::254]:80", "[fd00:ec2::254%1]:80"} {
		if err := dialControl("tcp", addr, nil); err == nil {
			t.Errorf("dial to %s was allowed", addr)
		}
	}
	if err := dialControl("tcp", "10.1.2.3:80", nil); err != nil {
		t.Errorf("a private address was refused: %v", err)
	}
}

func TestHTTPClientRefusesSchemeChangingRedirect(t *testing.T) {
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-https":
			http.Redirect(w, r, "https://"+r.Host+"/x", http.StatusFound)
		case "/to-magnet":
			w.Header().Set("Location", "magnet:?xt=urn:btih:abc&dn=x")
			w.WriteHeader(http.StatusFound)
		case "/same":
			http.Redirect(w, r, target.URL+"/done", http.StatusFound)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer target.Close()
	c := (Policy{}).HTTPClient(5 * time.Second)

	if _, err := c.Get(target.URL + "/to-https"); err == nil || !strings.Contains(err.Error(), "another scheme") {
		t.Errorf("scheme-changing redirect: err = %v", err)
	}
	_, err := c.Get(target.URL + "/to-magnet")
	var mr *ErrMagnetRedirect
	if !errors.As(err, &mr) || !strings.HasPrefix(mr.Magnet, "magnet:?xt=urn:btih:abc") {
		t.Errorf("magnet redirect was not surfaced as the source: %v", err)
	}
	resp, err := c.Get(target.URL + "/same")
	if err != nil {
		t.Fatalf("same-scheme redirect failed: %v", err)
	}
	resp.Body.Close()
}

func TestReadCapped(t *testing.T) {
	if b, err := ReadCapped(strings.NewReader("12345"), 5); err != nil || string(b) != "12345" {
		t.Fatalf("at the cap: %q %v", b, err)
	}
	if _, err := ReadCapped(strings.NewReader("123456"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the cap: %v, want ErrTooLarge", err)
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"https://idx.test/api?t=get&id=abc&apikey=SECRET":                        "https://idx.test/api?id=abc&t=get",
		"https://idx.test/dl/1?r=SECRET&file=x.torrent":                          "https://idx.test/dl/1?file=x.torrent",
		"https://idx.test/dl?PassKey=SECRET&token=SECRET2":                       "https://idx.test/dl",
		"https://user:SECRET@idx.test/x":                                         "https://user@idx.test/x",
		"https://idx.test/plain":                                                 "https://idx.test/plain",
		"magnet:?xt=urn:btih:abc&tr=https%3A%2F%2Ft.test%2Fa%3Fpasskey%3DSECRET": "magnet:?tr=https%3A%2F%2Ft.test%2Fa&xt=urn%3Abtih%3Aabc",
	}
	for in, want := range cases {
		got := Redact(in)
		if got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(got, "SECRET") {
			t.Errorf("Redact(%q) leaked a credential: %q", in, got)
		}
	}
	if got := Redact("http://[bad"); strings.Contains(got, "bad") {
		t.Errorf("an unparseable link must be withheld, got %q", got)
	}
}

func TestRedactErrorStripsTheURL(t *testing.T) {
	c := (Policy{}).HTTPClient(time.Second)
	_, err := c.Get("http://127.0.0.1:1/api?apikey=SECRET")
	if err == nil {
		t.Skip("port 1 unexpectedly answered")
	}
	if strings.Contains(RedactError(err).Error(), "SECRET") {
		t.Fatalf("redacted error still carries the key: %v", RedactError(err))
	}
}
