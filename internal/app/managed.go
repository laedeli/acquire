package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/laedeli/acquire/internal/store"
)

// Download clients the deployment runs itself.
//
// The addon chart can run qBittorrent and NZBGet next to acquire (its values
// clients.*). It declares them in ACQUIRE_MANAGED_CLIENTS: a JSON list of
// client writes in the shape the console sends — id, type, address, login,
// protocols, category and both folders — and acquire stores them at start, as
// managed clients, before its first push to the gateway:
//
//   - a declared client that is not stored yet is created;
//   - a stored one that differs from its declaration is brought in line;
//   - an admin's client with the same id and type is taken over (the first
//     client of a type is named after it, as the chart's are, so the downloads
//     and grabs recorded before keep resolving) — unless downloads are still
//     in flight on it: those finish where they started, and a later start
//     takes it over;
//   - a managed client the deployment no longer declares is removed, or only
//     disabled while downloads are still in flight on it.
//
// A managed client is always enabled, and its configuration follows the
// deployment: the API refuses to edit or remove it (ErrManaged), and testing
// it stays allowed. Without the variable the stored clients are left alone.

// ErrManaged refuses an API write to a client the deployment manages.
var ErrManaged = errors.New("this download client is managed by the deployment")

// managedActor is who the audit trail names for the deployment's writes.
const managedActor = "deployment"

// ManagedResult is what applying the declaration did, by client id.
type ManagedResult struct {
	Created, Updated, Removed []string
	// Disabled: no longer declared, kept while downloads are in flight on it.
	Disabled []string
	// Skipped: declared clients that were not stored, "id: reason".
	Skipped []string
}

func (r ManagedResult) String() string {
	var parts []string
	for _, p := range []struct {
		what string
		ids  []string
	}{{"created", r.Created}, {"updated", r.Updated}, {"removed", r.Removed}, {"disabled", r.Disabled}} {
		if len(p.ids) > 0 {
			parts = append(parts, p.what+" "+strings.Join(p.ids, ", "))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "in line")
	}
	return strings.Join(parts, "; ")
}

// managedWrite is one declared client: the row to store and its secret.
type managedWrite struct {
	client store.DownloadClient
	secret SecretInput
	// current is the stored row an update replaces.
	current *store.DownloadClient
}

// managedPlan is what brings the stored clients in line with a declaration.
type managedPlan struct {
	create, update []managedWrite
	remove         []store.DownloadClient
	skip           []string
}

// ApplyManagedClients stores the download clients the deployment declares. It
// runs once at start, before the first push; a client it cannot store is
// reported in the result and the others are still applied.
func (s *Service) ApplyManagedClients(ctx context.Context) (ManagedResult, error) {
	if !s.cfg.ManagedClientsSet || s.st == nil {
		return ManagedResult{}, nil
	}
	declared, ids, skipped, err := s.declaredClients(ctx, s.cfg.ManagedClients)
	if err != nil {
		return ManagedResult{}, err
	}
	stored, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		return ManagedResult{}, fmt.Errorf("cannot read download clients: %w", err)
	}
	busy := func(id string) (int, error) { return s.st.ActiveDownloadsOn(ctx, id) }
	plan, err := planManaged(declared, ids, stored, busy, s.storedSecretIs)
	if err != nil {
		return ManagedResult{}, err
	}
	plan.skip = append(skipped, plan.skip...)
	return s.applyManagedPlan(ctx, plan), nil
}

// parseManaged reads the declaration: a JSON list of client writes. Nothing
// is echoed from it in an error, since it carries credentials.
func parseManaged(raw string) ([]ClientInput, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var in []ClientInput
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("ACQUIRE_MANAGED_CLIENTS is not a JSON list of download clients: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("ACQUIRE_MANAGED_CLIENTS holds more than one JSON list")
	}
	return in, nil
}

// declaredClients validates the declaration with the console's own rules. It
// returns the clients to store, every id declared (valid or not: a stored
// managed client whose declaration is invalid is kept, not removed), and why
// the invalid ones were left out.
func (s *Service) declaredClients(ctx context.Context, raw string) ([]managedWrite, map[string]bool, []string, error) {
	inputs, err := parseManaged(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	types, _ := s.ClientTypes(ctx)
	var out []managedWrite
	var skipped []string
	ids := map[string]bool{}
	for _, in := range inputs {
		id := strings.TrimSpace(in.ID)
		switch {
		case id == "":
			// The deployment names its clients: a default id would depend on
			// what happens to be stored.
			skipped = append(skipped, "(no id): the deployment must name each client it declares")
			continue
		case ids[id]:
			skipped = append(skipped, id+": declared more than once")
			continue
		}
		ids[id] = true
		in.ID = id
		c, fe := s.validateClient(ctx, in, types, nil, nil)
		if len(fe) > 0 {
			msgs := make([]string, 0, len(fe))
			for _, f := range fe {
				msgs = append(msgs, f.Field+" "+f.Message)
			}
			skipped = append(skipped, id+": "+strings.Join(msgs, "; "))
			continue
		}
		c.Managed, c.Enabled = true, true
		w := managedWrite{client: c, secret: in.Secret}
		if c.Auth == "none" {
			w.secret = SecretInput{} // nothing to authenticate with, nothing to keep
		}
		out = append(out, w)
	}
	return out, ids, skipped, nil
}

// planManaged compares the declared clients with the stored ones. ids are all
// the ids the deployment declares; busy counts the downloads in flight on a
// stored client; sameSecret reports whether a stored client holds the
// declared secret.
func planManaged(declared []managedWrite, ids map[string]bool, stored []store.DownloadClient,
	busy func(id string) (int, error), sameSecret func(store.DownloadClient, SecretInput) bool) (managedPlan, error) {
	var p managedPlan
	byID := make(map[string]store.DownloadClient, len(stored))
	for _, c := range stored {
		byID[c.ID] = c
	}
	for _, w := range declared {
		cur, ok := byID[w.client.ID]
		switch {
		case !ok:
			p.create = append(p.create, w)
		case cur.Type != w.client.Type:
			p.skip = append(p.skip, fmt.Sprintf("%s: a %s client of that id is stored, not the deployment's %s",
				cur.ID, cur.Type, w.client.Type))
		case !cur.Managed:
			n, err := busy(cur.ID)
			if err != nil {
				return managedPlan{}, fmt.Errorf("cannot count the downloads on %s: %w", cur.ID, err)
			}
			if n > 0 {
				p.skip = append(p.skip, fmt.Sprintf("%s: the client stored under that id has %d download(s) in flight; "+
					"the deployment's takes it over at a start after they finish", cur.ID, n))
				continue
			}
			w.current = &cur
			p.update = append(p.update, w)
		case !sameManagedRow(cur, w.client) || !sameSecret(cur, w.secret):
			w.current = &cur
			p.update = append(p.update, w)
		}
	}
	for _, c := range stored {
		if c.Managed && !ids[c.ID] {
			p.remove = append(p.remove, c)
		}
	}
	return p, nil
}

// sameManagedRow compares everything a declaration sets, the secret aside.
func sameManagedRow(a, b store.DownloadClient) bool {
	return a.Type == b.Type && a.BaseURL == b.BaseURL && a.Auth == b.Auth && a.Username == b.Username &&
		slices.Equal(a.Protocols, b.Protocols) && a.Category == b.Category &&
		a.RemotePath == b.RemotePath && a.LocalPath == b.LocalPath &&
		a.Priority == b.Priority && a.Enabled == b.Enabled && a.Managed == b.Managed
}

// storedSecretIs reports whether c holds exactly the declared secret. A secret
// that cannot be opened (the key changed) is not the same: storing it again
// seals it with the current key.
func (s *Service) storedSecretIs(c store.DownloadClient, want SecretInput) bool {
	if !want.replaces() {
		return len(c.SecretCT) == 0
	}
	if len(c.SecretCT) == 0 {
		return false
	}
	table, id, field := store.ClientSecretAAD(c.ID)
	pt, err := s.box.Open(table, id, field, c.SecretCT, c.SecretKID)
	return err == nil && subtle.ConstantTimeCompare(pt, []byte(want.Value)) == 1
}

// applyManagedPlan writes the plan. Every write is the deployment's in the
// audit trail; one that fails is reported and the rest still run.
func (s *Service) applyManagedPlan(ctx context.Context, p managedPlan) ManagedResult {
	res := ManagedResult{Skipped: p.skip}
	fail := func(id string, err error) { res.Skipped = append(res.Skipped, id+": "+err.Error()) }
	for _, w := range p.create {
		secret, err := s.managedSecret(w)
		if err == nil {
			_, err = s.st.CreateDownloadClient(ctx, w.client, secret, managedActor)
		}
		if err != nil {
			fail(w.client.ID, err)
			continue
		}
		res.Created = append(res.Created, w.client.ID)
	}
	for _, w := range p.update {
		secret, err := s.managedSecret(w)
		if err == nil {
			_, err = s.st.UpdateDownloadClient(ctx, w.client, secret, w.current.Generation, managedActor)
		}
		if err != nil {
			fail(w.client.ID, err)
			continue
		}
		res.Updated = append(res.Updated, w.client.ID)
	}
	for _, c := range p.remove {
		err := s.st.DeleteDownloadClient(ctx, c.ID, c.Generation, managedActor)
		if errors.Is(err, store.ErrInFlight) {
			// Its downloads still need the client's folders to be found when
			// they complete: keep the row, but route nothing new to it.
			err = nil
			if c.Enabled {
				off := c
				off.Enabled = false
				_, err = s.st.UpdateDownloadClient(ctx, off, store.SecretWrite{Op: store.SecretKeep}, c.Generation, managedActor)
			}
			if err == nil {
				res.Disabled = append(res.Disabled, c.ID)
				continue
			}
		}
		if err != nil {
			fail(c.ID, err)
			continue
		}
		res.Removed = append(res.Removed, c.ID)
	}
	return res
}

// managedSecret is the secret write for a declared client: kept when the
// stored one is already the declared one, sealed anew when it is not, removed
// for a client that does not authenticate.
func (s *Service) managedSecret(w managedWrite) (store.SecretWrite, error) {
	if w.current != nil && s.storedSecretIs(*w.current, w.secret) {
		return store.SecretWrite{Op: store.SecretKeep}, nil
	}
	if w.secret.replaces() {
		return s.sealClientSecret(w.client.ID, w.secret)
	}
	if w.current != nil && len(w.current.SecretCT) > 0 {
		return store.SecretWrite{Op: store.SecretClear}, nil
	}
	return store.SecretWrite{Op: store.SecretKeep}, nil
}
