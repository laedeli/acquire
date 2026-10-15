package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/store"
)

// An automatic grab takes the episode alone, or one run of at most ten
// episodes in a row that are all missing. A longer range, a list that is not
// a run, and a run with an episode held, not aired yet, or reserved by
// another grab are for someone to pick.
func TestAutomaticGrabsTakeOneEpisodeOrOneShortRunOfMissing(t *testing.T) {
	ctx := context.Background()
	r := newTargetRig(t,
		"Show.Name.S01E01-E12.2160p.WEB-DL-G", "Show.Name.S01E01-E11.2160p.WEB-DL-G",
		"Show.Name.S01E01E03.2160p.WEB-DL-G", "Show.Name.S01E01E02.720p.WEB-DL-G")
	_, eps := track(t, r.svc, 4711, "Show Name", 12, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(g.TargetIDs)
	if g.ReleaseTitle != "Show.Name.S01E01E02.720p.WEB-DL-G" || !reflect.DeepEqual(g.TargetIDs, []string{eps[0].ID, eps[1].ID}) {
		t.Fatalf("took %q for %v", g.ReleaseTitle, g.TargetIDs)
	}

	for name, prepare := range map[string]func(r *targetRig, eps []store.Target){
		"held": func(r *targetRig, eps []store.Target) {
			_ = r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[1].ID, ItemID: "x"})
		},
		"not aired": func(r *targetRig, eps []store.Target) {
			later := time.Now().Add(72 * time.Hour)
			_ = r.svc.st.SetAirWindow(ctx, eps[1].ID, &later, 0)
		},
		"reserved": func(r *targetRig, eps []store.Target) {
			if _, err := r.svc.st.ReserveTargetGrab(ctx, store.TargetGrab{ID: "tg_other", TitleID: eps[1].TitleID},
				eps[1].ID, []string{eps[1].ID}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newTargetRig(t, "Show.Name.S01E01E02.720p.WEB-DL-G")
			_, eps := track(t, r.svc, 4712, "Show Name", 2, true)
			prepare(r, eps)
			if _, err := r.svc.GrabTarget(ctx, eps[0].ID, "sweep"); !errors.Is(err, errNoReleases) {
				t.Fatalf("a run with an episode %s: %v", name, err)
			}
			if st := target(t, r.svc, eps[0].ID); !strings.Contains(st.SearchDetail, "none an automatic grab takes") {
				t.Errorf("the search's outcome: %+v", st)
			}
		})
	}
}

// A file whose name lists episodes that are not one run cannot go over as
// one: it is left out, and the episodes it lists have no file. The same
// release's download with a file per episode maps each.
func TestAFileOfEpisodesNotInARowIsLeftOut(t *testing.T) {
	ep := func(e int) store.Target {
		s, n := 1, e
		return store.Target{ID: fmt.Sprintf("e%d", e), Kind: "episode", SeasonNumber: &s, EpisodeNumber: &n}
	}
	targets := []store.Target{ep(1), ep(3)}
	one := planEpisodes([]videoFile{{"/d/Show.S01E01E03.1080p.mkv", 900}}, targets, "Show.S01E01E03.1080p-G")
	if len(one.Files) != 0 || len(one.Missing) != 2 || len(one.Left) != 1 || !strings.Contains(one.Left[0], "not one run") {
		t.Errorf("one file of a list: %+v", one)
	}
	each := planEpisodes([]videoFile{{"/d/Show.S01E01.1080p.mkv", 900}, {"/d/Show.S01E03.1080p.mkv", 900}}, targets, "Show.S01E01E03.1080p-G")
	if len(each.Files) != 2 || len(each.Missing) != 0 {
		t.Errorf("a file per episode: %+v", each)
	}
}
