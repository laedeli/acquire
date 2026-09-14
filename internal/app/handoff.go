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
	IndexerID    *int64
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

	var payload []byte
	var payloadName string
	if isHTTP(link) && protocol != "http" {
		f, err := s.fetchRelease(ctx, link, protocol, h.Title)
		if err != nil {
			return handedOff{}, err
		}
		switch {
		case f.Magnet != "":
			link, protocol = f.Magnet, "torrent"
		case f.Kind == "":
			protocol = "http" // not a release file: a hoster link, handed over as is
		default:
			payload, payloadName, protocol = f.Body, f.Name, f.Kind
		}
	}

	client, err := s.resolveClient(ctx, h.ClientID, protocol)
	if err != nil {
		return handedOff{}, refused{err}
	}
	// Last gate before anything is written to disk. Fails closed.
	if err := s.AdmitGrab(ctx, client, h.Size); err != nil {
		return handedOff{}, refused{err}
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
	res, err := s.gw.Add(ctx, req)
	if errors.Is(err, gateway.ErrUnknownClient) && s.sync != nil {
		// The gateway restarted and has not been told its clients yet. Tell it
		// once and try once more; a second refusal is a real error.
		log.Printf("acquire: gateway does not know client %s; pushing configuration and retrying", client.ID)
		if _, perr := s.sync.Push(ctx); perr == nil {
			res, err = s.gw.Add(ctx, req)
		}
	}
	if err != nil {
		return handedOff{}, err
	}

	g := store.Grab{
		WantedID: h.WantedID, Adapter: client.ID, ClientJobID: res.ClientJobID,
		Source: endpoint.Redact(h.Link), ReleaseTitle: h.ReleaseTitle, Indexer: h.Indexer,
		Protocol: protocol, SizeBytes: h.Size, Seeders: h.Seeders, Reason: h.Reason,
		IndexerID: h.IndexerID, ReleaseGUID: h.ReleaseGUID,
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

// failGrab records a failed grab on the request, unless the grab was merely
// refused, which says nothing about the request.
func (s *Service) failGrab(ctx context.Context, wantedID string, err error) {
	var r refused
	if errors.As(err, &r) {
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
func (s *Service) fetchRelease(ctx context.Context, link, protocol, title string) (fetched, error) {
	if _, err := s.policy.Check(link); err != nil {
		return fetched{}, fmt.Errorf("release link refused: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fetched{}, errors.New("release link is not a valid URL")
	}
	req.Header.Set("User-Agent", "acquire")
	resp, err := s.fetch.Do(req)
	var mr *endpoint.ErrMagnetRedirect
	if errors.As(err, &mr) {
		return fetched{Kind: "torrent", Magnet: mr.Magnet}, nil
	}
	if err != nil {
		return fetched{}, fmt.Errorf("fetching the release: %w", endpoint.RedactError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fetched{}, fmt.Errorf("the source answered HTTP %d for the release file", resp.StatusCode)
	}

	head, err := endpoint.ReadCapped(io.LimitReader(resp.Body, sniffBytes), sniffBytes)
	if err != nil {
		return fetched{}, fmt.Errorf("reading the release: %w", endpoint.RedactError(err))
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
		// API limit looks exactly like this.
		return fetched{}, fmt.Errorf("the release link did not return %s", describeKind(want))
	}
	limit := int64(maxNZBBytes)
	if want == "torrent" {
		limit = maxTorrentBytes
	}
	rest, err := endpoint.ReadCapped(resp.Body, limit-int64(len(head)))
	if errors.Is(err, endpoint.ErrTooLarge) {
		return fetched{}, fmt.Errorf("the %s is larger than %d MiB", describeKind(want), limit>>20)
	}
	if err != nil {
		return fetched{}, fmt.Errorf("reading the release: %w", endpoint.RedactError(err))
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
	name = sanitizeFileName(strings.TrimSuffix(name, path.Ext(name)))
	if name == "" {
		name = "release"
	}
	return name + ext
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
