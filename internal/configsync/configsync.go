// Package configsync keeps the download gateway running the download clients
// acquire has stored.
//
// The gateway holds its api-sourced clients in memory only, so it forgets them
// on every restart; acquire's database is the record. The reconciler closes
// that gap from acquire's side, with one idempotent operation — replace the
// gateway's whole api-sourced set with what is stored — at three moments:
//
//   - at boot;
//   - immediately after every client write (the write path calls Push);
//   - every interval, when the gateway reports a revision other than acquire's
//     (it restarted, or a push was lost).
//
// A push the gateway refuses in part (an id that collides with one of its
// environment clients, say) leaves it reporting its previous revision, so the
// interval check would send the same refused set every 30 seconds for as long
// as nobody fixes it. That repeat backs off — one interval, doubling up to
// maxRetryWait — until acquire's revision or what the gateway reports running
// changes, and is logged once rather than on every attempt.
//
// The revision is acquire's: the highest client generation ever issued. It only
// moves forward, so "different" is enough to know the gateway is behind — as
// long as a revision is only ever sent with the clients it describes, which the
// store guarantees (ClientsConfig waits out a write in progress). "Equal" then
// means the gateway runs exactly what is stored.
//
// Credentials are decrypted only to build the request and never logged: log
// lines name client ids and revisions, nothing else.
package configsync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

// Store is what the reconciler reads. ClientsConfig must return a revision
// and the clients it describes as one consistent pair: the gateway reporting a
// revision is taken to mean it runs exactly those clients.
type Store interface {
	ClientsRevision(ctx context.Context) (int64, error)
	ClientsConfig(ctx context.Context) (int64, []store.DownloadClient, error)
}

// Gateway is the configuration half of the gateway client.
type Gateway interface {
	Enabled() bool
	ConfigClients(ctx context.Context) (gateway.ConfigState, error)
	PutConfigClients(ctx context.Context, body gateway.ConfigPut) (gateway.ApplyResult, error)
}

// Opener decrypts stored secrets.
type Opener interface {
	Open(table, id, field string, ct []byte, kid string) ([]byte, error)
}

// Status is what the reconciler last learned, for setup and health.
type Status struct {
	// Configured: a gateway address is set at all.
	Configured bool `json:"configured"`
	// Checked: at least one attempt has completed since boot.
	Checked bool `json:"checked"`
	// Reachable: the last call reached the gateway.
	Reachable bool `json:"reachable"`
	// ConfigAPI: the gateway offers /api/v1/config (false after a 404 there).
	ConfigAPI bool `json:"configApi"`
	// Revision is acquire's; GatewayRevision is what the gateway last reported
	// or accepted.
	Revision        int64 `json:"revision"`
	GatewayRevision int64 `json:"gatewayRevision"`
	// Applied and Errors are from the last push.
	Applied []string             `json:"applied"`
	Errors  []gateway.ApplyError `json:"errors"`
	// LastError is the last failure to check or push, "" after a success.
	LastError string    `json:"lastError,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	PushedAt  time.Time `json:"pushedAt"`
	// Clients is what the gateway last reported running, env clients included.
	Clients []gateway.ConfigClientView `json:"-"`
}

// InSync reports whether the gateway runs acquire's revision without errors.
func (s Status) InSync() bool {
	return s.Reachable && s.ConfigAPI && s.GatewayRevision == s.Revision && len(s.Errors) == 0
}

// maxRetryWait caps how long the interval check waits before sending a
// configuration the gateway refused again.
const maxRetryWait = 10 * time.Minute

// Reconciler pushes stored clients to the gateway.
type Reconciler struct {
	st    Store
	gw    Gateway
	box   Opener
	every time.Duration
	now   func() time.Time

	pushMu     sync.Mutex // one push at a time, so revisions arrive in order
	lastFailed string     // the last push outcome logged with errors (guarded by pushMu)

	mu      sync.RWMutex
	status  Status
	refused *refusal
}

// refusal is a push the gateway applied only in part. The interval check does
// not send the same set again before retryAt, unless something moved.
type refusal struct {
	revision        int64 // acquire's revision, not taken
	gatewayRevision int64 // the one the gateway kept reporting instead
	// running is what the gateway reported running at the first check after
	// the push (seen once it has); a change there — a restart, its environment
	// edited — is worth a push straight away.
	running []gateway.ConfigClientView
	seen    bool
	wait    time.Duration
	retryAt time.Time
}

// New returns a reconciler. every <= 0 means 30 seconds.
func New(st Store, gw Gateway, box Opener, every time.Duration) *Reconciler {
	if every <= 0 {
		every = 30 * time.Second
	}
	return &Reconciler{st: st, gw: gw, box: box, every: every, now: time.Now,
		status: Status{Configured: gw != nil && gw.Enabled(), ConfigAPI: true}}
}

// Run pushes once, then checks on every interval until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	if r.gw == nil || !r.gw.Enabled() {
		return
	}
	// A gateway that is down or too old fails every 30 s; say so when the
	// failure starts or changes, not twice a minute forever.
	var last string
	report := func(what string, err error) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if msg != last && ctx.Err() == nil {
			if err != nil {
				log.Printf("acquire: configsync %s: %v", what, err)
			} else if last != "" {
				log.Printf("acquire: configsync %s: recovered", what)
			}
		}
		last = msg
	}
	_, err := r.Push(ctx)
	report("boot push", err)
	t := time.NewTicker(r.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			report("check", r.Check(ctx))
		}
	}
}

// Status returns a copy of what the reconciler last learned.
func (r *Reconciler) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.status
	s.Applied = append([]string(nil), s.Applied...)
	s.Errors = append([]gateway.ApplyError(nil), s.Errors...)
	s.Clients = append([]gateway.ConfigClientView(nil), s.Clients...)
	return s
}

// Check asks the gateway which revision it runs and pushes when it differs.
func (r *Reconciler) Check(ctx context.Context) error {
	if r.gw == nil || !r.gw.Enabled() {
		return errors.New("download gateway not configured")
	}
	rev, err := r.st.ClientsRevision(ctx)
	if err != nil {
		return err
	}
	state, err := r.gw.ConfigClients(ctx)
	r.record(func(s *Status) {
		s.Checked, s.CheckedAt, s.Revision = true, time.Now(), rev
		r.noteCallResult(s, err)
		if err == nil {
			s.GatewayRevision = state.Revision
			s.Clients = state.Clients
		}
	})
	if err != nil {
		return err
	}
	if state.Revision == rev || r.holdRefused(rev, state) {
		return nil
	}
	// Push logs the revision it sends when it succeeds; a failure comes back
	// to Run, which logs it once.
	_, err = r.Push(ctx)
	return err
}

// holdRefused reports whether the gateway still stands where it did after
// refusing rev, and the wait before offering it again has not run out.
func (r *Reconciler) holdRefused(rev int64, state gateway.ConfigState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.refused
	if p == nil || p.revision != rev || p.gatewayRevision != state.Revision {
		return false
	}
	if !p.seen {
		p.running, p.seen = state.Clients, true
	} else if !reflect.DeepEqual(p.running, state.Clients) {
		return false
	}
	return r.now().Before(p.retryAt)
}

// noteOutcome remembers a push the gateway refused in part, doubling the wait
// when it refuses the same revision again, and forgets it after a clean push.
func (r *Reconciler) noteOutcome(rev int64, res gateway.ApplyResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(res.Errors) == 0 || res.Revision == rev {
		r.refused = nil
		return
	}
	wait := r.every
	if p := r.refused; p != nil && p.revision == rev && p.gatewayRevision == res.Revision {
		wait = min(2*p.wait, maxRetryWait)
	}
	r.refused = &refusal{revision: rev, gatewayRevision: res.Revision, wait: wait, retryAt: r.now().Add(wait)}
}

// Push replaces the gateway's api-sourced clients with every enabled stored
// client. If any secret cannot be opened nothing is pushed: sending a partial
// set would REMOVE the unreadable clients from a running gateway, turning a
// key-rotation mistake into stopped downloads.
func (r *Reconciler) Push(ctx context.Context) (gateway.ApplyResult, error) {
	r.pushMu.Lock()
	defer r.pushMu.Unlock()
	if r.gw == nil || !r.gw.Enabled() {
		return gateway.ApplyResult{}, errors.New("download gateway not configured")
	}
	rev, clients, err := r.st.ClientsConfig(ctx)
	if err != nil {
		return gateway.ApplyResult{}, err
	}
	body, err := r.desired(rev, clients)
	if err != nil {
		r.record(func(s *Status) { s.Revision, s.LastError = rev, err.Error() })
		return gateway.ApplyResult{}, err
	}
	res, err := r.gw.PutConfigClients(ctx, body)
	r.record(func(s *Status) {
		s.Checked, s.CheckedAt, s.Revision = true, time.Now(), rev
		r.noteCallResult(s, err)
		if err == nil {
			s.PushedAt = s.CheckedAt
			s.GatewayRevision = res.Revision
			s.Applied, s.Errors = res.Applied, res.Errors
		}
	})
	if err != nil {
		return gateway.ApplyResult{}, err
	}
	r.noteOutcome(rev, res)
	failed := make([]string, 0, len(res.Errors))
	for _, e := range res.Errors {
		failed = append(failed, e.ID)
	}
	// A clean push is worth a line every time: it is a write, a boot or a
	// repaired gateway. The same refusal again is not.
	line := fmt.Sprintf("revision %d: applied %v, failed %v", rev, res.Applied, failed)
	if len(failed) == 0 || line != r.lastFailed {
		log.Printf("acquire: configsync pushed %s", line)
	}
	r.lastFailed = ""
	if len(failed) > 0 {
		r.lastFailed = line
	}
	return res, nil
}

// desired builds the PUT body from stored clients.
func (r *Reconciler) desired(rev int64, clients []store.DownloadClient) (gateway.ConfigPut, error) {
	body := gateway.ConfigPut{Revision: rev, Clients: []gateway.ConfigClient{}}
	for _, c := range clients {
		if !c.Enabled {
			continue
		}
		cc := gateway.ConfigClient{
			ID: c.ID, Type: c.Type, BaseURL: c.BaseURL, Auth: c.Auth, Username: c.Username,
			Category: c.Category, SavePath: c.RemotePath, Protocols: c.Protocols,
		}
		if c.Auth != "none" && len(c.SecretCT) > 0 {
			if r.box == nil {
				return gateway.ConfigPut{}, fmt.Errorf("client %s: no key to open its secret", c.ID)
			}
			table, id, field := store.ClientSecretAAD(c.ID)
			pt, err := r.box.Open(table, id, field, c.SecretCT, c.SecretKID)
			if err != nil {
				return gateway.ConfigPut{}, fmt.Errorf("client %s: stored secret cannot be opened: %w", c.ID, err)
			}
			cc.Secret = string(pt)
		}
		body.Clients = append(body.Clients, cc)
	}
	sort.Slice(body.Clients, func(i, j int) bool { return body.Clients[i].ID < body.Clients[j].ID })
	return body, nil
}

func (r *Reconciler) noteCallResult(s *Status, err error) {
	switch {
	case err == nil:
		s.Reachable, s.ConfigAPI, s.LastError = true, true, ""
	case errors.Is(err, gateway.ErrNoConfigAPI):
		s.Reachable, s.ConfigAPI, s.LastError = true, false, err.Error()
	default:
		s.Reachable, s.LastError = false, err.Error()
	}
}

func (r *Reconciler) record(fn func(*Status)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.status)
}
