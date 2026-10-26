package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/store"
)

// Every searched target records what its search found and when it is due
// again, so the sweep works through the whole backlog: the second run takes
// the targets the first did not, and a find is not searched again for hours.
func TestTheSweepRotatesAndRecordsEveryOutcome(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 12, true)

	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	searched := func() (n int) {
		for _, e := range eps {
			if target(t, r.svc, e.ID).LastSearchAt != nil {
				n++
			}
		}
		return n
	}
	if n := searched(); n != 10 {
		t.Fatalf("first sweep searched %d, want 10", n)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if n := searched(); n != 12 {
		t.Fatalf("after the second sweep %d of 12 were ever searched — the sweep returned the same targets", n)
	}
	one := target(t, r.svc, eps[0].ID)
	if one.SearchOutcome != store.SearchFound || one.NextSearchAt == nil || time.Until(*one.NextSearchAt) < 11*time.Hour ||
		!strings.Contains(one.SearchDetail, "Show.Name.S01E01") {
		t.Errorf("a find: %+v", one)
	}
	two := target(t, r.svc, eps[1].ID)
	if two.SearchOutcome != store.SearchNone || two.SearchFailures != 1 || two.NextSearchAt == nil {
		t.Errorf("a miss: %+v", two)
	}
	if len(r.gw.added()) != 0 {
		t.Errorf("the sweep grabbed with autoGrab off: %d", len(r.gw.added()))
	}
	if due, _ := r.svc.st.DueTargets(ctx, 50); len(due) != 0 {
		t.Errorf("right after a full turn nothing is due: %d", len(due))
	}
}

// The sweep grabs only with search.autoGrab, which is off by default; even
// then never a target whose holdings are unknown, and never more than the
// downloads the policy allows at once.
func TestAutoGrabIsGated(t *testing.T) {
	r := newTargetRig(t,
		"Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G",
		"Other.Show.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	if (&Service{}).envSearchSettings().AutoGrab {
		t.Fatal("autoGrab is on by default")
	}
	doc, err := r.svc.GetSearchSettings(ctx)
	if err != nil || doc.AutoGrab {
		t.Fatalf("stored settings: %+v %v", doc, err)
	}
	_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
	_, unknown := track(t, r.svc, 99, "Other Show", 1, false)

	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(r.gw.added()) != 0 || target(t, r.svc, eps[0].ID).SearchOutcome != store.SearchFound {
		t.Fatalf("with autoGrab off the sweep searches and grabs nothing (adds %d)", len(r.gw.added()))
	}
	if target(t, r.svc, unknown[0].ID).LastSearchAt != nil {
		t.Error("a target of unknown holdings was searched")
	}

	// On, with one download at once.
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 1, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	policy := ""
	for _, sec := range r.svc.Setup(ctx).Sections {
		if sec.Key == "grab-policy" {
			policy = sec.Summary
		}
	}
	if !strings.Contains(policy, "the sweep grabs what is missing") {
		t.Errorf("setup's grab policy: %q", policy)
	}
	for _, e := range eps {
		// Due again right away, as if the recheck had passed.
		if err := r.svc.st.RecordSearch(ctx, e.ID, store.SearchRecord{Outcome: store.SearchFound, Again: time.Millisecond}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	adds := r.gw.added()
	if len(adds) != 1 {
		t.Fatalf("grabbed %d with a cap of one", len(adds))
	}
	first, second := target(t, r.svc, eps[0].ID), target(t, r.svc, eps[1].ID)
	if first.State != store.TargetGrabbed {
		t.Errorf("the first find: %+v", first)
	}
	if second.State != store.TargetWanted || !strings.Contains(second.SearchDetail, "found; not grabbed yet: 1 download(s) already in flight (cap 1)") ||
		time.Until(*second.NextSearchAt) > time.Hour {
		t.Errorf("the second find waits for the next sweep: %+v", second)
	}
	if st := target(t, r.svc, unknown[0].ID); st.State != store.TargetWanted || st.LastSearchAt != nil {
		t.Errorf("a target of unknown holdings: %+v", st)
	}
	// And the gate itself, should one reach it.
	tg := target(t, r.svc, unknown[0].ID)
	if _, ok := r.svc.autoGrabAllowed(ctx, tg); ok {
		t.Error("autoGrabAllowed let a target of unknown holdings through")
	}
	if _, ok := r.svc.autoGrabAllowed(ctx, second); !ok {
		t.Error("autoGrabAllowed refused a wanted target of known holdings")
	}
}

// Once the cap stops a sweep's grabs, every later target the policy would
// take is found again on the next sweep, not only the one that met the cap:
// before, the third find of a run waited twelve hours.
func TestEveryFindAfterTheCapWaitsForTheNextSweep(t *testing.T) {
	r := newTargetRig(t,
		"Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G",
		"Show.Name.S01E03.1080p.WEB-DL.x265-G", "Show.Name.S01E04.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4712, "Show Name", 4, true)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 1, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if adds := r.gw.added(); len(adds) != 1 {
		t.Fatalf("grabbed %d with a cap of one", len(adds))
	}
	for _, e := range eps[1:] {
		st := target(t, r.svc, e.ID)
		if st.State != store.TargetWanted || !strings.Contains(st.SearchDetail, "found; not grabbed yet: 1 download(s) already in flight (cap 1)") ||
			st.NextSearchAt == nil || time.Until(*st.NextSearchAt) > time.Hour {
			t.Errorf("%s waits for the next sweep: %+v", e.ID, st)
		}
	}
}

// With only the new things grabbed on their own, the old backlog a stopped
// sweep finds keeps its twelve hours: it is not grabbed on its own, so
// searching it every sweep would spend the sources' allowance for nothing.
func TestTheOldBacklogAfterTheCapKeepsItsWait(t *testing.T) {
	r := newTargetRig(t,
		"Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G",
		"Show.Name.S01E03.1080p.WEB-DL.x265-G", "Show.Name.S01E04.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4713, "Show Name", 4, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-300), 0)
	for _, e := range eps[1:] {
		_ = r.svc.st.SetAirWindow(ctx, e.ID, day(-3), 0)
	}
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 1, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if adds := r.gw.added(); len(adds) != 1 {
		t.Fatalf("grabbed %d with a cap of one", len(adds))
	}
	var grabbed, waiting int
	for _, e := range eps[1:] {
		switch st := target(t, r.svc, e.ID); {
		case st.State == store.TargetGrabbed:
			grabbed++
		case st.State == store.TargetWanted && st.NextSearchAt != nil && time.Until(*st.NextSearchAt) <= time.Hour:
			waiting++
		default:
			t.Errorf("a new episode after the cap: %+v", st)
		}
	}
	if grabbed != 1 || waiting != 2 {
		t.Errorf("new episodes: %d grabbed, %d waiting for the next sweep", grabbed, waiting)
	}
	old := target(t, r.svc, eps[0].ID)
	if old.State != store.TargetWanted || old.SearchOutcome != store.SearchFound || old.NextSearchAt == nil ||
		time.Until(*old.NextSearchAt) < 11*time.Hour {
		t.Errorf("the old backlog keeps its twelve hours: %+v", old)
	}
}

// A refusal that lasts until someone acts — here the storage floor — leaves
// only the target that met it due on the next sweep; every later find keeps
// its twelve hours, so the sources are not asked every sweep for grabs that
// cannot happen.
func TestALastingRefusalKeepsTheLaterFindsAtTwelveHours(t *testing.T) {
	r := newTargetRig(t,
		"Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G",
		"Show.Name.S01E03.1080p.WEB-DL.x265-G", "Show.Name.S01E04.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4714, "Show Name", 4, true)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true, StorageFloorGB: 2000}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if adds := r.gw.added(); len(adds) != 0 {
		t.Fatalf("grabbed %d below the floor", len(adds))
	}
	first := target(t, r.svc, eps[0].ID)
	if first.State != store.TargetWanted || !strings.Contains(first.SearchDetail, "found; not grabbed yet: refusing grab:") ||
		first.NextSearchAt == nil || time.Until(*first.NextSearchAt) > time.Hour {
		t.Errorf("the target that met the refusal waits for the next sweep: %+v", first)
	}
	for _, e := range eps[1:] {
		st := target(t, r.svc, e.ID)
		if st.State != store.TargetWanted || st.SearchOutcome != store.SearchFound || st.NextSearchAt == nil ||
			time.Until(*st.NextSearchAt) < 11*time.Hour {
			t.Errorf("%s keeps its twelve hours: %+v", e.ID, st)
		}
	}
}
