package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/opensubtitles"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/subtitles"
)

// What the console's subtitles tab reads and does.

// The statuses a title's language is in, as the console says them.
const (
	SubStatusHas       = "has"       // has subtitles
	SubStatusWay       = "way"       // on its way: in today's downloads, or being searched now
	SubStatusWaiting   = "waiting"   // waiting: not searched yet, or a match waits for a later day's downloads
	SubStatusNotFound  = "notfound"  // not found, looked for again later
	SubStatusAttention = "attention" // needs attention: a download or the attach failed
)

// SubtitleLanguage is one of the instance's languages and how many titles
// have it.
type SubtitleLanguage struct {
	Tag   string `json:"tag"`
	Name  string `json:"name"`
	Has   int    `json:"has"`
	Total int    `json:"total"`
}

// SubtitleBudget is the day's downloads: the automatic budget, what it used
// today (UTC) and has left, the picks a person made today, what the account
// said, and the downloads the account has left today — the one number the
// console shows, and what a pick takes from (nil while it is not known).
type SubtitleBudget struct {
	Daily       int                  `json:"daily"`
	Used        int                  `json:"used"`
	Left        int                  `json:"left"`
	Manual      int                  `json:"manual"`
	Account     *opensubtitles.Quota `json:"account,omitempty"`
	AccountLeft *int                 `json:"accountLeft"`
}

// SubtitleOverview is what the tab says of the work as a whole.
type SubtitleOverview struct {
	// Source is the subtitle source's name, as the console shows it.
	Source     string                `json:"source"`
	Enabled    bool                  `json:"enabled"`
	Configured bool                  `json:"configured"`
	Missing    []string              `json:"missing,omitempty"`
	Problem    string                `json:"problem,omitempty"`
	Languages  []SubtitleLanguage    `json:"languages"`
	From       SubtitleLanguagesFrom `json:"from"`
	Budget     SubtitleBudget        `json:"budget"`
	Settings   SubtitleSettings      `json:"settings"`
	Counts     map[string]int        `json:"counts"`
}

// SubtitleOverview reads the overview; lang narrows the counts.
func (s *Service) SubtitleOverview(ctx context.Context, lang string) SubtitleOverview {
	o := SubtitleOverview{Source: opensubtitles.Name, Enabled: s.cfg.SubtitlesEnabled, Configured: s.subs.client.Enabled(),
		Missing: s.subs.client.Missing(), Settings: s.SubtitleSettings(ctx), Counts: map[string]int{}}
	s.subs.mu.Lock()
	o.Problem = s.subs.problem
	s.subs.mu.Unlock()
	langs, from, err := s.SubtitleLanguages(ctx)
	if err != nil && o.Problem == "" && o.Configured {
		o.Problem = err.Error()
	}
	o.From = from
	cov, _ := s.st.SubtitleCoverageByLanguage(ctx)
	byLang := map[string]store.SubtitleCoverage{}
	for _, c := range cov {
		byLang[c.Language] = c
	}
	for _, l := range langs {
		o.Languages = append(o.Languages, SubtitleLanguage{Tag: l, Name: subtitles.Name(l), Has: byLang[l].Has, Total: byLang[l].Total})
	}
	if o.Languages == nil {
		o.Languages = []SubtitleLanguage{}
	}
	left, used := s.BudgetLeft(ctx)
	manual, _ := s.st.SubtitleDownloadsSince(ctx, dayStart(time.Now()), "manual")
	o.Budget = SubtitleBudget{Daily: s.cfg.SubtitleDailyBudget, Used: used, Left: left, Manual: manual}
	if q := s.subs.client.LastQuota(); q.Known {
		o.Budget.Account = &q
	}
	if n, ok := s.AccountLeft(ctx, false); ok {
		o.Budget.AccountLeft = &n
	}
	counts, err := s.st.SubtitleStateCounts(ctx, lang)
	if err == nil {
		o.Counts = tabCounts(counts, left, o.Settings.Auto)
	}
	return o
}

// tabCounts are the tabs' counts from the states': a match within today's
// downloads, and a language asked for and not searched yet, are on their
// way; the other matches and the languages not searched yet are waiting.
func tabCounts(c map[string]int, left int, auto bool) map[string]int {
	found, askedFound := c[store.SubtitleFound], c[store.SubtitleFound+":asked"]
	today := 0
	if auto {
		today = min(found, left)
	} else {
		today = min(askedFound, left)
	}
	asked := c[store.SubtitleWanted+":asked"]
	return map[string]int{
		SubStatusAttention: c[store.SubtitleFailed],
		SubStatusNotFound:  c[store.SubtitleNotFound],
		SubStatusWay:       today + asked,
		SubStatusWaiting:   c[store.SubtitleWanted] - asked + found - today,
		SubStatusHas:       c[store.SubtitleHas],
	}
}

// SubtitleItem is one title and language as the console lists it: its
// status in words, the line under it, and what its details say.
type SubtitleItem struct {
	ItemID   string     `json:"itemId"`
	Kind     string     `json:"kind"`
	Title    string     `json:"title"`
	TitleID  string     `json:"titleId"`
	Year     int        `json:"year"`
	Season   int        `json:"season"`
	Episode  int        `json:"episode"`
	AiredAt  *time.Time `json:"airedAt"`
	Language string     `json:"language"`
	// LanguageName is the language as people say it ("German").
	LanguageName string `json:"languageName"`
	Status       string `json:"status"`
	Detail       string `json:"detail"`
	// Why is what the details say: the source's or the catalog's own words.
	Why     string     `json:"why,omitempty"`
	NextAt  *time.Time `json:"nextAt"`
	HasKind string     `json:"hasKind,omitempty"`
	// Removable: a subtitle acquire attached, which it can remove.
	Removable  bool                 `json:"removable"`
	Preview    bool                 `json:"preview"`
	Release    string               `json:"release,omitempty"`
	Match      string               `json:"match,omitempty"`
	AttachedAt *time.Time           `json:"attachedAt,omitempty"`
	Found      *subtitles.Candidate `json:"found,omitempty"`
	Asked      bool                 `json:"asked"`
	Original   string               `json:"original,omitempty"`
}

// tabStates are the stored states a tab is made of.
var tabStates = map[string][]string{
	SubStatusAttention: {store.SubtitleFailed},
	SubStatusNotFound:  {store.SubtitleNotFound},
	SubStatusWay:       {store.SubtitleFound, store.SubtitleWanted},
	SubStatusWaiting:   {store.SubtitleFound, store.SubtitleWanted},
	SubStatusHas:       {store.SubtitleHas},
}

// SubtitleList lists a tab's titles and languages (every one for "" ), of a
// language ("" every one) and words of the title or S04E07, at most limit.
func (s *Service) SubtitleList(ctx context.Context, tab, lang, q string, limit int) ([]SubtitleItem, error) {
	rows, err := s.st.SubtitleRows(ctx, store.SubtitleFilter{States: tabStates[tab], Language: lang, Query: q, Limit: limit})
	if err != nil {
		return nil, err
	}
	rank, err := s.st.FoundSubtitleRank(ctx)
	if err != nil {
		return nil, err
	}
	left, _ := s.BudgetLeft(ctx)
	auto := s.SubtitleSettings(ctx).Auto
	out := make([]SubtitleItem, 0, len(rows))
	for _, r := range rows {
		it := s.subtitleItem(r, rank, left, auto)
		if tab != "" && it.Status != tab {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// subtitleItem says a row in the console's words.
func (s *Service) subtitleItem(r store.SubtitleRow, rank map[store.SubtitleKey]int, left int, auto bool) SubtitleItem {
	w := r.Want
	it := SubtitleItem{ItemID: r.ItemID, Kind: r.Kind, Title: r.Title, TitleID: r.TitleID, Year: r.Year, Season: r.Season,
		Episode: r.Episode, AiredAt: r.AiredAt, Language: w.Language, LanguageName: subtitles.Name(w.Language),
		NextAt: w.NextAt, HasKind: w.HasKind, Asked: w.AskedAt != nil, Release: w.Release, Match: w.Match,
		AttachedAt: w.AttachedAt, Preview: w.Preview != "", Removable: w.SubtitleID != ""}
	if r.Kind == "episode" && r.TitleID == "" {
		it.TitleID = "series:" + strings.ToLower(r.Title)
	}
	if r.OriginalNote != "" {
		it.Original = r.OriginalNote
	}
	if len(w.Found) > 0 {
		var c subtitles.Candidate
		if json.Unmarshal(w.Found, &c) == nil {
			it.Found = &c
		}
	}
	switch w.State {
	case store.SubtitleHas:
		it.Status = SubStatusHas
		switch {
		case w.HasKind == "traditional" && w.SubtitleID != "":
			it.Detail = "added by acquire · in Traditional characters"
		case w.HasKind == "traditional":
			it.Detail = "came with the file · in Traditional characters"
		case w.HasKind == "ai" && w.SubtitleID != "":
			it.Detail = "added by acquire · machine-translated · " + matchWords(w.Match)
		case w.HasKind == "ai":
			it.Detail = "made by AI"
		case w.SubtitleID != "":
			it.Detail = "added by acquire · " + matchWords(w.Match)
		case w.HasKind == "attached":
			it.Detail = "added separately"
		default:
			it.Detail = "came with the file"
		}
	case store.SubtitleFound:
		// the line says what happens next; how the match fits is in its details
		pos, queued := rank[store.SubtitleKey{ItemID: r.ItemID, Language: w.Language}] // not: the title takes none now
		inToday := queued && pos < left && (auto || w.AskedAt != nil)
		it.Why = w.Detail
		if inToday {
			it.Status, it.Detail = SubStatusWay, "found · in today's downloads"
		} else {
			it.Status = SubStatusWaiting
			switch {
			case !queued:
				it.Detail = "found · can't be added to this title yet"
				if r.NotAttachable != "" {
					it.Why = r.NotAttachable
				}
			case !auto && w.AskedAt == nil:
				it.Detail = "found · automatic downloads are off: pick it to download"
			case s.cfg.SubtitleDailyBudget <= 0:
				it.Detail = "found · automatic downloads are set to 0 a day"
			default:
				days := 1 + (pos-left)/s.cfg.SubtitleDailyBudget
				it.Detail = fmt.Sprintf("found · waits for the daily download limit (about %d %s)", days, plural(days, "day", "days"))
			}
		}
	// the other lines: what keeps the language from the title, in plain words,
	// then what happens next and when ("can't read the file · tries another
	// version in 5h"); the source's or the catalog's own words are its details
	case store.SubtitleWanted:
		it.Why = w.Detail
		switch {
		case w.AskedAt != nil:
			it.Status, it.Detail = SubStatusWay, "asked for · searched next"
		case w.Trouble != "":
			it.Status, it.Detail = SubStatusWaiting, w.Trouble+" · "+nextInLine(w.NextAt)
		default:
			it.Status, it.Detail = SubStatusWaiting, "not searched yet"
		}
	case store.SubtitleNotFound:
		trouble := w.Trouble
		if trouble == "" {
			trouble = "nothing found"
		}
		it.Status, it.Detail, it.Why = SubStatusNotFound, trouble+" · "+again(w.NextAt), w.Detail
	case store.SubtitleFailed:
		trouble := w.Trouble
		if trouble == "" {
			trouble = "it failed"
		}
		it.Status, it.Why = SubStatusAttention, w.Detail
		switch {
		case w.NextAt == nil:
			it.Detail = fmt.Sprintf("%s · failed %d times in a row", trouble, w.Failures)
		case w.Retry == store.SubtitleRetryAnother:
			it.Detail = trouble + " · tries another version " + inWords(time.Until(*w.NextAt))
		case w.Retry == store.SubtitleRetryNone:
			it.Detail = trouble + " · no other version yet · " + again(w.NextAt)
		default:
			it.Detail = trouble + " · " + again(w.NextAt)
		}
	}
	// a Chinese language of one script, the title having a subtitle in the
	// other: a fact, so it follows the line's first fact and comes before what
	// happens next ("nothing found · has one in Traditional characters · again
	// in 14 days")
	if w.State != store.SubtitleHas && w.OtherScript != "" {
		has := "has one in " + characters(w.OtherScript)
		if first, rest, ok := strings.Cut(it.Detail, " · "); ok {
			it.Detail = first + " · " + has + " · " + rest
		} else {
			it.Detail += " · " + has
		}
	}
	return it
}

// characters names a Chinese script: "Traditional characters".
func characters(script string) string {
	if script == "hans" {
		return "Simplified characters"
	}
	return "Traditional characters"
}

// again says when a want comes again: "again in 6h", "again in 3 days".
func again(next *time.Time) string {
	if next == nil {
		return "again later"
	}
	return "again " + inWords(time.Until(*next))
}

// nextInLine says when a wanted language is searched: in line now, or again
// at its time.
func nextInLine(next *time.Time) string {
	if next == nil || !next.After(time.Now()) {
		return "searched next"
	}
	return again(next)
}

// matchWords says how a subtitle fits the title's file.
func matchWords(m string) string {
	switch m {
	case subtitles.MatchFile:
		return "matches this file"
	case subtitles.MatchRelease:
		return "matches this release"
	}
	return "may be out of sync"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// inWords says a wait: "in 6h", "in 3 days", "soon".
func inWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "soon"
	case d < time.Hour:
		return fmt.Sprintf("in %dmin", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()+0.5))
	}
	return fmt.Sprintf("in %d days", int(d.Hours()/24+0.5))
}

// SubtitleSearch is a person's search for one title and language: the
// candidates in the order an automatic choice ranks them, each saying how it
// fits and why one is never taken on its own; what is known of the file; and
// the account's downloads.
type SubtitleSearch struct {
	// Source is the name of the source searched.
	Source     string                `json:"source"`
	ItemID     string                `json:"itemId"`
	Title      string                `json:"title"`
	Language   string                `json:"language"`
	Name       string                `json:"languageName"`
	Hash       bool                  `json:"hash"`
	Release    string                `json:"release,omitempty"`
	Original   string                `json:"original,omitempty"`
	Candidates []subtitles.Candidate `json:"candidates"`
	// Left is the downloads the account has left today, the one number the
	// console shows (AccountLeft); using a candidate takes one of them. Nil
	// while it is not known.
	Left  *int                 `json:"left"`
	Quota *opensubtitles.Quota `json:"quota,omitempty"`
}

// ErrSubtitlesOff is any subtitle action while the work cannot run.
var ErrSubtitlesOff = errors.New("subtitles are off: switched off, or the subtitle source isn't set up")

// ErrNoDownloadsLeft is a pick while the account has no downloads left today.
var ErrNoDownloadsLeft = errors.New("the account has no downloads left today")

// SearchSubtitleTitle searches one title in one language for a person: the
// catalog asked again (and its fingerprint read when it was not), then the
// source asked, every candidate kept. A search costs no download.
func (s *Service) SearchSubtitleTitle(ctx context.Context, k store.SubtitleKey) (SubtitleSearch, error) {
	if !s.subtitlesOn() {
		return SubtitleSearch{}, ErrSubtitlesOff
	}
	t, err := s.st.SubtitleTitleByID(ctx, k.ItemID)
	if err != nil {
		return SubtitleSearch{}, err
	}
	w, err := s.st.SubtitleWantOf(ctx, k)
	if err != nil {
		return SubtitleSearch{}, err
	}
	langs, _, err := s.SubtitleLanguages(ctx)
	if err != nil {
		return SubtitleSearch{}, err
	}
	if _, t, err = s.readSubtitleCatalog(ctx, t, langs); err != nil {
		return SubtitleSearch{}, err
	}
	out := SubtitleSearch{Source: opensubtitles.Name, ItemID: t.ItemID, Title: t.Title, Language: k.Language, Name: subtitles.Name(k.Language),
		Hash: t.Sum64 != "", Release: t.Release, Original: t.OriginalNote, Candidates: []subtitles.Candidate{}}
	if t.Kind == "episode" {
		out.Title = fmt.Sprintf("%s S%02dE%02d", t.Title, t.Season, t.Episode)
	}
	results, err := s.searchResults(ctx, subtitleQuery(t, []string{k.Language}))
	if err != nil {
		return SubtitleSearch{}, err
	}
	out.Candidates = rankFor(results, k.Language, t, w.Blocked, s.SubtitleSettings(ctx))
	if n, ok := s.AccountLeft(ctx, true); ok {
		out.Left = &n
	}
	if q := s.subs.client.LastQuota(); q.Known {
		out.Quota = &q
	}
	return out, nil
}

// PickSubtitle downloads the candidate a person picked and attaches it, at
// once: a download from the account's own, not from the automatic budget, so
// a pick works whatever the budget used — ErrNoDownloadsLeft only when the
// account has none left today. It takes the place of the one acquire
// attached in the language before, which is removed from the catalog once
// the new one is listed, its file never taken again for the title.
func (s *Service) PickSubtitle(ctx context.Context, k store.SubtitleKey, c subtitles.Candidate) error {
	if !s.subtitlesOn() {
		return ErrSubtitlesOff
	}
	t, err := s.st.SubtitleTitleByID(ctx, k.ItemID)
	if err != nil {
		return err
	}
	before, err := s.st.SubtitleWantOf(ctx, k)
	if err != nil {
		return err
	}
	if ok, how := subtitles.Serves(c.Language, "", k.Language); !ok || c.FileID == 0 {
		return fmt.Errorf("that version isn't a %s subtitle", subtitles.Name(k.Language))
	} else {
		c.Traditional, c.Tag = how == "hant", subtitles.TagOf(c.Language)
	}
	if !t.Attachable {
		return fmt.Errorf("this title can't take a subtitle right now: %s", t.NotAttachable)
	}
	if n, ok := s.AccountLeft(ctx, true); ok && n <= 0 {
		return ErrNoDownloadsLeft
	}
	if err := s.attachCandidate(ctx, t, k.Language, c, "manual"); err != nil {
		var qe *opensubtitles.QuotaError
		if errors.As(err, &qe) {
			return fmt.Errorf("%w: %v", ErrNoDownloadsLeft, qe)
		}
		return err
	}
	s.notify()
	if before.SubtitleID == "" {
		return nil
	}
	if now, err := s.st.SubtitleWantOf(ctx, k); err != nil || now.SubtitleID == before.SubtitleID {
		return err // the same file picked again: the catalog answered with the one there is
	}
	if err := s.kc.DetachSubtitle(ctx, k.ItemID, before.SubtitleID); err != nil {
		return fmt.Errorf("the new one is attached, and the one it replaces could not be removed: %w", err)
	}
	if before.FileID != 0 && before.FileID != c.FileID {
		return s.st.BlockSubtitleFile(ctx, k, before.FileID)
	}
	return nil
}

// RemoveSubtitle removes the subtitle acquire attached for a title's
// language: the catalog asked to, the file it was made of never taken again
// for it, another looked for after a day — none in a language that is no
// longer the instance's, whose want goes with it.
func (s *Service) RemoveSubtitle(ctx context.Context, k store.SubtitleKey) error {
	w, err := s.st.SubtitleWantOf(ctx, k)
	if err != nil {
		return err
	}
	if w.SubtitleID == "" {
		return fmt.Errorf("acquire added no %s subtitle to this title", subtitles.Name(k.Language))
	}
	if err := s.kc.DetachSubtitle(ctx, k.ItemID, w.SubtitleID); err != nil {
		return err
	}
	if langs, _, lerr := s.SubtitleLanguages(ctx); lerr == nil && !slices.Contains(langs, k.Language) {
		err = s.st.DropSubtitleWant(ctx, k)
	} else {
		err = s.st.ForgetAttachedSubtitle(ctx, k, time.Now().Add(subtitleRemovedWait),
			"removed in the console; searched again from "+time.Now().Add(subtitleRemovedWait).UTC().Format("2006-01-02 15:04 UTC"))
	}
	if err == nil {
		s.notify()
	}
	return err
}

// FindSubtitles puts titles' languages first in line, searched on the next
// pass and their matches downloaded first; it answers those it put there.
func (s *Service) FindSubtitles(ctx context.Context, keys []store.SubtitleKey) ([]store.SubtitleKey, error) {
	if !s.subtitlesOn() {
		return nil, ErrSubtitlesOff
	}
	asked, err := s.st.AskSubtitles(ctx, keys)
	if err != nil {
		return nil, err
	}
	s.wakeSubtitles()
	s.notify()
	return asked, nil
}

// SubtitlePreview is the beginning of the subtitle acquire attached.
func (s *Service) SubtitlePreview(ctx context.Context, k store.SubtitleKey) (string, error) {
	w, err := s.st.SubtitleWantOf(ctx, k)
	if err != nil {
		return "", err
	}
	return w.Preview, nil
}

// subtitlesSection is the setup checklist's subtitles: off, needing the
// account and key, failing, or working with the day's downloads.
func (s *Service) subtitlesSection(ctx context.Context) SetupSection {
	sec := SetupSection{Key: "subtitles"}
	switch {
	case !s.cfg.SubtitlesEnabled:
		sec.State, sec.Summary = SetupReady, "switched off (subtitles.enabled)"
		return sec
	case !s.subs.client.Enabled():
		sec.State = SetupNeedsSetup
		sec.Summary = opensubtitles.Name + " needs " + strings.Join(s.subs.client.Missing(), ", ") + " (subtitles.opensubtitles.*)"
		return sec
	}
	s.subs.mu.Lock()
	problem := s.subs.problem
	s.subs.mu.Unlock()
	if problem != "" {
		sec.State, sec.Summary = SetupDegraded, problem
		return sec
	}
	langs, _, _ := s.SubtitleLanguages(ctx)
	names := make([]string, 0, len(langs))
	for _, l := range langs {
		names = append(names, subtitles.Name(l))
	}
	sec.State = SetupReady
	sec.Summary = opensubtitles.Name + " · " + strings.Join(names, ", ")
	if n, ok := s.AccountLeft(ctx, false); ok {
		sec.Summary += fmt.Sprintf(" · %d %s left today", n, plural(n, "download", "downloads"))
	}
	return sec
}
