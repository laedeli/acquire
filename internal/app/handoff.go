package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

// Size caps for release files acquire fetches itself. An NZB for a large
// season pack runs to tens of MiB; a .torrent is metadata only.
const (
	maxNZBBytes     = 50 << 20
	maxTorrentBytes = 10 << 20
	sniffBytes      = 64 << 10
)

// handoff is one release on its way to a download client.
type handoff struct {
	WantedID string
	Title    string
	// Link is a magnet, a release-file URL (which may carry a search source's
	// credential) or a hoster link.
	Link string
	// Protocol is usenet, torrent or http; "" means decide from the link.
	Protocol string
	// ClientID forces a client (a manual grab naming one); "" routes by
	// protocol.
	ClientID string

	// Provenance recorded with the grab.
	ReleaseTitle string
	Indexer      string
	IndexerID    int64 // the search source that offered it; 0 for a link an admin supplied
	ReleaseGUID  string
	Size         int64
	Seeders      *int32
	Reason       string
}

// handedOff is where a release went.
type handedOff struct {
	Client   store.DownloadClient
	JobID    string
	Protocol string
}

// handOff routes a release to a client and gives it to the gateway.
//
// Release files are fetched HERE and passed as content. Handing the client the
// link instead would give it the search source's API key, which is embedded in
// the link: the key would then sit in the client's history, its logs and its
// API responses, outside anything acquire controls. Magnets and hoster links
// carry no such credential and go as the source.
func (s *Service) handOff(ctx context.Context, h handoff) (handedOff, error) {
	link := strings.TrimSpace(h.Link)
	protocol := h.Protocol
	if protocol == "" {
		protocol = inferProtocol(link)
	}

	// When the protocol is known up front, route and admit before anything is
	// spent at the source: a release file fetched for a grab that is then
	// refused still counts against the source's daily downloads.
	var client store.DownloadClient
	routed := false
	route := func() error {
		c, err := s.resolveClient(ctx, h.ClientID, protocol)
		if err != nil {
			return refused{err}
		}
		// Last gate before anything is written to disk. Fails closed.
		if err := s.AdmitGrab(ctx, c, h.Size, protocol); err != nil {
			return refused{err}
		}
		client, routed = c, true
		return nil
	}
	if protocol != "" {
		if err := route(); err != nil {
			return handedOff{}, err
		}
	}

	var payload []byte
	var payloadName string
	if isHTTP(link) && protocol != "http" {
		if h.IndexerID > 0 {
			if err := s.spendGrab(ctx, h.IndexerID); err != nil {
				return handedOff{}, err
			}
		}
		f, err := s.fetchRelease(ctx, link, protocol, h.Title)
		if err != nil {
			return handedOff{}, err
		}
		kind := protocol
		switch {
		case f.Magnet != "":
			link, kind = f.Magnet, "torrent"
		case f.Kind == "":
			kind = "http" // not a release file: a hoster link, handed over as is
		default:
			payload, payloadName, kind = f.Body, f.Name, f.Kind
			if kind == "usenet" && store.IsTargetGrab(h.WantedID) {
				if name := releasePayloadName(h.ReleaseTitle); name != "" {
					payloadName = name
				}
			}
		}
		if kind != protocol {
			protocol, routed = kind, false
		}
	}
	if !routed {
		if err := route(); err != nil {
			return handedOff{}, err
		}
	}

	req := gateway.AddRequest{
		Adapter: client.ID, Client: client.ID, Title: h.Title, WantedItemID: h.WantedID,
		SavePath: client.RemotePath, Category: client.Category,
	}
	if payload != nil {
		req.PayloadB64 = base64.StdEncoding.EncodeToString(payload)
		req.PayloadName = payloadName
	} else {
		req.Source = link
	}
	if err := ctx.Err(); err != nil {
		// The caller went away before anything reached the gateway.
		return handedOff{}, unavailable{error: err}
	}
	// From the add on, nothing the caller does cuts it short: once the gateway
	// may have the release, what follows is the only record of its job.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), addTimeout)
	defer cancel()
	res, err := s.gw.Add(actx, req)
	if errors.Is(err, gateway.ErrUnknownClient) && s.sync != nil {
		// The gateway restarted and has not been told its clients yet. Tell it
		// once and try once more; a second refusal is a real error.
		log.Printf("acquire: gateway does not know client %s; pushing configuration and retrying", client.ID)
		if _, perr := s.sync.Push(actx); perr == nil {
			res, err = s.gw.Add(actx, req)
		}
	}
	if err != nil {
		return handedOff{}, addFailed(err)
	}
	ctx = actx
	if store.IsTargetGrab(h.WantedID) {
		// A grab bound to targets keeps where it came from on its own row
		// (target_grabs, written by grabRelease); grabs belongs to requests.
		return handedOff{Client: client, JobID: res.ClientJobID, Protocol: protocol}, nil
	}

	g := store.Grab{
		WantedID: h.WantedID, Adapter: client.ID, ClientJobID: res.ClientJobID,
		Source: endpoint.Redact(h.Link), ReleaseTitle: h.ReleaseTitle, Indexer: h.Indexer,
		Protocol: protocol, SizeBytes: h.Size, Seeders: h.Seeders, Reason: h.Reason,
		ReleaseGUID: h.ReleaseGUID,
	}
	if h.IndexerID > 0 {
		id := h.IndexerID
		g.IndexerID = &id
	}
	if s.box.Enabled() && h.Link != "" {
		table, id, field := store.GrabAAD(g.WantedID, g.Adapter, g.ClientJobID)
		if ct, kid, err := s.box.Seal(table, id, field, []byte(h.Link)); err == nil {
			g.SourceCT, g.SourceKID = ct, kid
		}
	}
	if err := s.st.RecordGrabRelease(ctx, g); err != nil {
		log.Printf("acquire: record grab %s/%s: %v", client.ID, res.ClientJobID, err)
	}
	return handedOff{Client: client, JobID: res.ClientJobID, Protocol: protocol}, nil
}

// refused is a grab turned away before anything reached a client: no client for
// the protocol, or admission (disk, concurrency). The request itself is fine
// and stays as it was; only the operator's answer changes.
type refused struct{ error }

func (r refused) Unwrap() error { return r.error }

// What else a hand-over that did not happen says, for a grab bound to
// targets (targetgrab.go) — a request's grab fails on any of them, as before.
//
// unavailable: the source or the gateway could not be had for now — down,
// failing, busy, timing out, refusing acquire's credential, or the caller
// gone before anything was sent. It says nothing about the release or what it
// is for: the targets stay wanted and are searched again after a while.
type unavailable struct {
	error
	// gateway is set when it is the gateway, or the client behind it: no
	// other release fares better then.
	gateway bool
}

func (e unavailable) Unwrap() error { return e.error }

// badRelease: the release itself cannot be had — its file is gone from the
// source, too large, a link the policy refuses, or the client refused it. It
// is blocked for the targets, and the next release is taken.
type badRelease struct{ error }

func (e badRelease) Unwrap() error { return e.error }

// answerLost: the add was sent but its answer did not come back, so the
// gateway may have handed the release to its client. Nothing is undone: the
// job's events bind the grab, or the stale-grab sweep gives it up.
type answerLost struct{ error }

func (e answerLost) Unwrap() error { return e.error }

// gatewayRefused: the gateway answered and took nothing for a reason of its
// configuration — it does not know the client, the client is being removed,
// the add was malformed. Nothing about the release or its targets.
type gatewayRefused struct{ error }

func (e gatewayRefused) Unwrap() error { return e.error }

// addTimeout bounds one add and what records it, run on a context of its own
// once begun: longer than the gateway client's own wait for a large payload.
const addTimeout = 5 * time.Minute

// addFailed says what an add the gateway did not take means.
func addFailed(err error) error {
	var ae *gateway.AddError
	switch {
	case errors.Is(err, gateway.ErrAnswerLost):
		return answerLost{err}
	case errors.Is(err, gateway.ErrUnreachable), errors.Is(err, gateway.ErrGatewayBusy):
		return unavailable{error: err, gateway: true}
	case errors.As(err, &ae) && ae.Rejected():
		return badRelease{err}
	case errors.As(err, &ae) && ae.Temporary():
		return unavailable{error: err, gateway: true}
	}
	return gatewayRefused{err}
}

// failGrab records a failed grab on the request, unless the grab was merely
// refused, which says nothing about the request: a release waiting for room
// only says why on it, in its own state.
func (s *Service) failGrab(ctx context.Context, wantedID string, err error) {
	var r refused
	if errors.As(err, &r) {
		var room noRoom
		if errors.As(err, &room) {
			if w, gerr := s.st.GetWanted(ctx, wantedID); gerr == nil {
				s.setStatus(ctx, wantedID, w.Status, clip(err.Error(), 300))
			}
		}
		return
	}
	s.setStatus(ctx, wantedID, "failed", "grab failed: "+err.Error())
}

// resolveClient honours an explicitly named client, else routes by protocol.
func (s *Service) resolveClient(ctx context.Context, id, protocol string) (store.DownloadClient, error) {
	if id == "" {
		return s.pickClient(ctx, protocol)
	}
	c, err := s.st.GetDownloadClient(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.DownloadClient{}, fmt.Errorf("no download client %q — add it under download clients", id)
	}
	if err != nil {
		return store.DownloadClient{}, err
	}
	if !c.Enabled {
		return store.DownloadClient{}, fmt.Errorf("download client %s is disabled", id)
	}
	if protocol != "" && !contains(c.Protocols, protocol) {
		return store.DownloadClient{}, fmt.Errorf("download client %s does not handle %s", id, protocol)
	}
	return c, nil
}

// inferProtocol reads what a link is from its shape alone.
func inferProtocol(link string) string {
	if strings.HasPrefix(strings.ToLower(link), "magnet:") {
		return "torrent"
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".torrent":
		return "torrent"
	case ".nzb":
		return "usenet"
	}
	return ""
}

func isHTTP(link string) bool {
	l := strings.ToLower(link)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// fetched is a release file (or what the link turned out to be).
type fetched struct {
	Kind   string // usenet | torrent | "" (not a release file)
	Body   []byte
	Name   string
	Magnet string
}

// fetchRelease downloads a release file under the endpoint policy.
//
// protocol "" means the link's kind is unknown (a manual grab): the first bytes
// decide, and a link that is not a release file comes back with Kind "" so it
// can be handed over as a hoster link instead of being read to the cap.
//
// What a fetch that failed says: badRelease for the release itself (gone from
// the source, too large, a link the policy refuses), unavailable for the
// source (out of reach, failing, rate-limiting, refusing the key, or an error
// page where the file should be).
func (s *Service) fetchRelease(ctx context.Context, link, protocol, title string) (fetched, error) {
	if _, err := s.policy.Check(link); err != nil {
		return fetched{}, badRelease{fmt.Errorf("release link refused: %w", err)}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fetched{}, badRelease{errors.New("release link is not a valid URL")}
	}
	req.Header.Set("User-Agent", "acquire")
	resp, err := s.fetch.Do(req)
	var mr *endpoint.ErrMagnetRedirect
	if errors.As(err, &mr) {
		return fetched{Kind: "torrent", Magnet: mr.Magnet}, nil
	}
	if err != nil {
		return fetched{}, unavailable{error: fmt.Errorf("fetching the release: %w", endpoint.RedactError(err))}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("the source answered HTTP %d for the release file", resp.StatusCode)
		switch c := resp.StatusCode; {
		case c == http.StatusTooManyRequests, c == http.StatusRequestTimeout, c >= 500,
			c == http.StatusUnauthorized, c == http.StatusForbidden:
			return fetched{}, unavailable{error: err}
		}
		return fetched{}, badRelease{err}
	}

	head, err := endpoint.ReadCapped(io.LimitReader(resp.Body, sniffBytes), sniffBytes)
	if err != nil {
		return fetched{}, unavailable{error: fmt.Errorf("reading the release: %w", endpoint.RedactError(err))}
	}
	kind := sniffRelease(head)
	if protocol == "" && kind == "" {
		return fetched{}, nil
	}
	want := protocol
	if want == "" {
		want = kind
	}
	if kind != want {
		// Typically an HTML or JSON error page served with 200 — an exhausted
		// API limit looks exactly like this: the source's, not the release's.
		return fetched{}, unavailable{error: fmt.Errorf("the release link did not return %s", describeKind(want))}
	}
	limit := int64(maxNZBBytes)
	if want == "torrent" {
		limit = maxTorrentBytes
	}
	rest, err := endpoint.ReadCapped(resp.Body, limit-int64(len(head)))
	if errors.Is(err, endpoint.ErrTooLarge) {
		return fetched{}, badRelease{fmt.Errorf("the %s is larger than %d MiB", describeKind(want), limit>>20)}
	}
	if err != nil {
		return fetched{}, unavailable{error: fmt.Errorf("reading the release: %w", endpoint.RedactError(err))}
	}
	body := append(head, rest...)
	return fetched{Kind: want, Body: body, Name: payloadName(resp, link, title, want)}, nil
}

// sniffRelease identifies an NZB or a .torrent from its first bytes.
func sniffRelease(b []byte) string {
	t := bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
	// A .torrent is a bencoded dictionary: "d", then a length-prefixed key such
	// as "8:announce" or "4:info". Nothing an error page starts with looks
	// like that.
	if len(t) > 2 && t[0] == 'd' && t[1] >= '1' && t[1] <= '9' {
		if i := bytes.IndexByte(t[:min(len(t), 8)], ':'); i > 1 {
			return "torrent"
		}
	}
	if bytes.Contains(bytes.ToLower(t), []byte("<nzb")) {
		return "usenet"
	}
	return ""
}

func describeKind(kind string) string {
	if kind == "torrent" {
		return "a .torrent file"
	}
	return "an NZB"
}

// payloadName is the file name the client sees: the server's, else the link's,
// else the title — always with the right extension and nothing path-like.
func payloadName(resp *http.Response, link, title, kind string) string {
	ext := ".nzb"
	if kind == "torrent" {
		ext = ".torrent"
	}
	var name string
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	if name == "" {
		if u, err := url.Parse(link); err == nil && strings.EqualFold(path.Ext(u.Path), ext) {
			name = path.Base(u.Path)
		}
	}
	if name == "" {
		name = title
	}
	name = payloadBase(name)
	if name == "" {
		name = "release"
	}
	return name + ext
}

// payloadBase is what payloadName keeps of a name for the file: without what
// looks like its extension, cleaned and cut (sanitizeFileName).
func payloadBase(name string) string {
	return sanitizeFileName(strings.TrimSuffix(name, path.Ext(name)))
}

// releasePayloadName is the file name a target grab's NZB is handed over
// under, whatever the source calls the file: its release's, cleaned and cut
// as payloadName cleans and cuts a name, but whole — a release's name is no
// file name with an extension. NZBGet names the job after the NZB, and that
// name is how acquire tells the grab's job from another under the same id
// (jobIsGrabs). "" for a release with no name left.
func releasePayloadName(release string) string {
	name := sanitizeFileName(release)
	if name == "" {
		return ""
	}
	return name + ".nzb"
}

func sanitizeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r < 0x20:
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.Trim(s, ". ")
}

// mapClientPaths translates file paths from where the client saved them to
// where acquire sees the same folder. A path outside the client's folder is
// left as reported.
func mapClientPaths(files []string, remote, local string) []string {
	if remote == "" || local == "" {
		return files
	}
	prefix := strings.TrimRight(remote, `/\`)
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f
		if !strings.HasPrefix(f, prefix) {
			continue
		}
		rest := f[len(prefix):]
		if rest != "" && rest[0] != '/' && rest[0] != '\\' {
			continue // /downloads2 is not inside /downloads
		}
		out[i] = path.Join(local, strings.ReplaceAll(rest, `\`, "/"))
	}
	return out
}
