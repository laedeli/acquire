package release

import (
	_ "embed"
	"encoding/json"
	"sort"
	"testing"
)

// The corpus is 676 de-duplicated releases the incumbent automation actually
// grabbed in production. Scoring them is the only cheap way to find out whether
// a profile change quietly starts refusing real releases — which is exactly the
// bug that proposed size minima would have introduced (they would have rejected
// 126 of these).
//
// Honest limitation: this corpus is WINNERS ONLY. It measures whether we still
// accept what was chosen, not whether we would have chosen the same thing. It
// cannot catch a false ACCEPT. A negative corpus is separate work.
//
//go:embed testdata/prod_grabs.json
var prodGrabsJSON []byte

type prodGrab struct {
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	SizeMb   int64  `json:"sizeMb"`
	Indexer  string `json:"indexer"`
}

func loadCorpus(t *testing.T) []prodGrab {
	t.Helper()
	var out []prodGrab
	if err := json.Unmarshal(prodGrabsJSON, &out); err != nil {
		t.Fatalf("corpus: %v", err)
	}
	if len(out) < 500 {
		t.Fatalf("corpus shrank to %d — did the fixture get truncated?", len(out))
	}
	return out
}

// maxRejections pins today's number. Raising it is a deliberate act: it means
// the profile now refuses releases the incumbent was happy to grab, and the
// commit that raises it has to say why.
const maxRejections = 3

func TestDefaultProfileAcceptsWhatProductionActuallyGrabbed(t *testing.T) {
	corpus := loadCorpus(t)
	p := DefaultProfile()

	var rejected []string
	byReason := map[string]int{}
	for _, g := range corpus {
		v, _ := Score(Candidate{
			Title: g.Title, Protocol: g.Protocol, SizeMb: g.SizeMb, Seeders: 20,
		}, p)
		if v.Rejected {
			rejected = append(rejected, g.Title+"  ->  "+v.Summary())
			if len(v.Reasons) > 0 {
				byReason[v.Reasons[0]]++
			}
		}
	}

	if len(rejected) > maxRejections {
		sort.Strings(rejected)
		t.Errorf("the profile rejects %d of %d real production grabs (limit %d)",
			len(rejected), len(corpus), maxRejections)
		for _, r := range rejected {
			t.Logf("  %s", r)
		}
		return
	}
	t.Logf("rejected %d/%d", len(rejected), len(corpus))
	for reason, n := range byReason {
		t.Logf("  %3d  %s", n, reason)
	}
}

// A corpus of real releases is also the best available check that parsing is
// not silently degrading: if resolution or source stops being recognised, every
// downstream score is wrong while every test still passes.
func TestParserRecognisesTheProductionCorpus(t *testing.T) {
	corpus := loadCorpus(t)
	var noRes, noSource, noCodec int
	for _, g := range corpus {
		in := Parse(g.Title)
		if in.Resolution == "" {
			noRes++
		}
		if in.Source == "" {
			noSource++
		}
		if in.Codec == "" {
			noCodec++
		}
	}
	// Pinned at measured values with a little headroom; these are assertions
	// about the parser, not about the corpus.
	if noRes*100/len(corpus) > 10 {
		t.Errorf("resolution unparsed on %d/%d releases", noRes, len(corpus))
	}
	if noSource*100/len(corpus) > 15 {
		t.Errorf("source unparsed on %d/%d releases", noSource, len(corpus))
	}
	t.Logf("unparsed: resolution %d, source %d, codec %d (of %d)",
		noRes, noSource, noCodec, len(corpus))
}

// No real grab may parse as a cam. If one does, the reject rule is too broad
// and is about to start refusing legitimate releases in production.
func TestNoProductionGrabParsesAsCam(t *testing.T) {
	for _, g := range loadCorpus(t) {
		if in := Parse(g.Title); in.Source == "cam" {
			t.Errorf("false positive: %q parsed as cam", g.Title)
		}
	}
}
