package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/opensubtitles"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/subtitles"
)

// Subtitles in the instance's languages for the titles the library holds:
// for every packaged title acquire looks at, each of the instance's
// languages it has no full subtitle in is searched for at OpenSubtitles.com
// (its file's hash, its identities), the best match is downloaded within the
// day's budget and attached to the title through the catalog's attach seam
// (origin provider), where every player lists and plays it.
//
// The work runs in one worker: new arrivals first (catalog.item.packaged),
// then what was asked for, then the rest of the library, the newest first. A
// search costs no download, so the worker searches ahead of the budget; the
// day's downloads go to the matches in that order. A title nothing is found
// for is looked at again only after days. What the budget does not reach
// waits for a later day; what no source has stays not found — nothing else
// makes subtitles.

// The work's pace and patience.
const (
	// subtitleBatch is how many titles one pass searches.
	subtitleBatch = 20
	// subtitleFill is how many titles one pass takes on from the targets.
	subtitleFill = 500
	// subtitleNotFoundWait is the wait after the first search that found
	// nothing; it doubles with each, up to subtitleNotFoundMax.
	subtitleNotFoundWait = 7 * 24 * time.Hour
	subtitleNotFoundMax  = 28 * 24 * time.Hour
	// subtitleFailWait is the wait after a failed download or attach.
	subtitleFailWait = 6 * time.Hour
	// subtitleRemovedWait is how long a language whose subtitle a person
	// removed waits before another is looked for.
	subtitleRemovedWait = 24 * time.Hour
	// subtitleLanguagesTTL is how long the platform's languages are kept.
	subtitleLanguagesTTL = time.Hour
	// subtitlePreviewCues is how much of a subtitle the console previews.
	subtitlePreviewCues = 12
	// subtitlePause is the pause between two titles of a pass.
	subtitlePause = 2 * time.Second
)

// subtitleWork is the subtitle worker's state on the service.
type subtitleWork struct {
	client *opensubtitles.Client
	wake   chan struct{}
	pass   sync.Mutex // one pass at a time

	mu        sync.Mutex
	langs     []string              // the languages in use
	langsFrom SubtitleLanguagesFrom // where they are set
	langsAt   time.Time
	ensured   string // the languages every title has wants for
	problem   string // the last problem with the account or the catalog, for setup
	signedIn  bool
}

func newSubtitleWork(c *opensubtitles.Client) *subtitleWork {
	return &subtitleWork{client: c, wake: make(chan struct{}, 1)}
}

// SubtitleSettings are the subtitle settings the console edits: whether the
// work downloads on its own (else it only searches, and a person picks), and
// whether a machine- or AI-translated subtitle may be taken on its own.
type SubtitleSettings struct {
	Auto                   bool  `json:"auto"`
	AllowMachineTranslated bool  `json:"allowMachineTranslated"`
	Revision               int64 `json:"revision"`
}

const subtitleSettingsKey = "subtitles"

// SubtitleSettings reads the stored settings; downloading on its own and no
// machine translations, when none are stored.
func (s *Service) SubtitleSettings(ctx context.Context) SubtitleSettings {
	out := SubtitleSettings{Auto: true}
	st, err := s.st.GetSetting(ctx, subtitleSettingsKey)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(st.Value, &out)
	out.Revision = st.Revision
	return out
}

// PutSubtitleSettings replaces the settings (ifRevision > 0: only over that
// revision, store.ErrStale otherwise).
func (s *Service) PutSubtitleSettings(ctx context.Context, in SubtitleSettings, ifRevision int64, actor string) (SubtitleSettings, error) {
	st, err := s.st.PutSetting(ctx, subtitleSettingsKey, map[string]bool{"auto": in.Auto,
		"allowMachineTranslated": in.AllowMachineTranslated}, ifRevision, actor)
	if err != nil {
		return SubtitleSettings{}, err
	}
	out := SubtitleSettings{Revision: st.Revision}
	_ = json.Unmarshal(st.Value, &out)
	s.wakeSubtitles()
	return out, nil
}

// subtitlesOn reports whether the work can run: turned on, with an account
// and a key.
func (s *Service) subtitlesOn() bool {
	return s.cfg.SubtitlesEnabled && s.subs != nil && s.subs.client.Enabled()
}

func (s *Service) wakeSubtitles() {
	if s.subs == nil {
		return
	}
	select {
	case s.subs.wake <- struct{}{}:
	default:
	}
}

func (s *Service) subtitleProblem(p string) {
	s.subs.mu.Lock()
	s.subs.problem = p
	s.subs.mu.Unlock()
}

// RunSubtitles runs the subtitle work until ctx ends: a pass when the
// schedule, a packaged title or the console asks, and at least every half
// hour. Turned off, or without an account and a key, it says so once and
// does nothing.
func (s *Service) RunSubtitles(ctx context.Context) {
	switch {
	case !s.cfg.SubtitlesEnabled:
		log.Printf("acquire: subtitles are off (subtitles.enabled)")
		return
	case s.subs == nil || !s.subs.client.Enabled():
		log.Printf("acquire: subtitles are off: OpenSubtitles.com needs %s (subtitles.opensubtitles.* in acquire's installation)",
			strings.Join(s.subs.client.Missing(), ", "))
		return
	}
	if _, err := s.subs.client.Login(ctx); err != nil {
		log.Printf("acquire: subtitles: %v", err)
		s.subtitleProblem(err.Error())
	} else {
		s.subs.mu.Lock()
		s.subs.signedIn, s.subs.problem = true, ""
		s.subs.mu.Unlock()
		if _, _, err := s.subs.client.Account(ctx); err != nil {
			log.Printf("acquire: subtitles: the account: %v", err)
		}
	}
	tick := time.NewTicker(30 * time.Minute)
	defer tick.Stop()
	for {
		s.SubtitlePass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.subs.wake:
		case <-tick.C:
		}
	}
}

// SubtitleLanguagesFrom is where the instance's languages are set: for the
// whole platform (the catalog's setting), or in acquire's deployment (its
// subtitles.languages).
type SubtitleLanguagesFrom struct {
	Scope   string `json:"scope"`   // platform | deployment
	Setting string `json:"setting"` // the setting that names them
}

// SubtitleLanguages are the languages every title is to have a subtitle in:
// the deployment's (subtitles.languages), else the platform's, which the
// catalog names (kept an hour); where they are set; and why there are none.
func (s *Service) SubtitleLanguages(ctx context.Context) ([]string, SubtitleLanguagesFrom, error) {
	if len(s.cfg.SubtitleLanguages) > 0 {
		return s.cfg.SubtitleLanguages, SubtitleLanguagesFrom{Scope: "deployment", Setting: "subtitles.languages"}, nil
	}
	s.subs.mu.Lock()
	if len(s.subs.langs) > 0 && time.Since(s.subs.langsAt) < subtitleLanguagesTTL {
		langs, from := s.subs.langs, s.subs.langsFrom
		s.subs.mu.Unlock()
		return langs, from, nil
	}
	s.subs.mu.Unlock()
	langs, setting, err := s.kc.SubtitleLanguages(ctx)
	if err != nil {
		s.subs.mu.Lock()
		defer s.subs.mu.Unlock()
		if len(s.subs.langs) > 0 {
			return s.subs.langs, s.subs.langsFrom, nil // what the platform said last
		}
		return nil, SubtitleLanguagesFrom{}, fmt.Errorf("the platform's languages could not be read: %w", err)
	}
	if len(langs) == 0 {
		return nil, SubtitleLanguagesFrom{}, errors.New("no subtitle languages are set: packager.language_whitelist and subtitles.languages are both empty")
	}
	from := SubtitleLanguagesFrom{Scope: "platform", Setting: setting}
	s.subs.mu.Lock()
	s.subs.langs, s.subs.langsFrom, s.subs.langsAt = langs, from, time.Now()
	s.subs.mu.Unlock()
	return langs, from, nil
}

// SubtitlePass is one pass of the work: the languages in use, the titles
// taken on from the targets, the day's downloads spent on the matches in
// line, then a batch of searches, each match downloaded at once while the
// budget lasts. With downloading on its own off, the downloads go to what a
// person asked for alone.
func (s *Service) SubtitlePass(ctx context.Context) {
	if !s.subtitlesOn() || !s.subs.pass.TryLock() {
		return
	}
	defer s.subs.pass.Unlock()
	langs, _, err := s.SubtitleLanguages(ctx)
	if err != nil {
		s.subtitleProblem(err.Error())
		log.Printf("acquire: subtitles: %v", err)
		return
	}
	if key := strings.Join(langs, ","); key != s.subs.ensured {
		if err := s.st.EnsureSubtitleWants(ctx, langs); err != nil {
			log.Printf("acquire: subtitles: the languages' wants: %v", err)
			return
		}
		s.subs.ensured = key
	}
	if n, err := s.st.FillSubtitleTitles(ctx, subtitleFill, langs); err != nil {
		log.Printf("acquire: subtitles: the titles of the library: %v", err)
	} else if n > 0 {
		log.Printf("acquire: subtitles: %d more titles of the library to look at", n)
	}
	s.AccountLeft(ctx, true) // the account's count of today, which the console shows
	set := s.SubtitleSettings(ctx)
	downloaded := s.spendSubtitleBudget(ctx, set, langs)
	due, err := s.st.DueSubtitleSearches(ctx, subtitleBatch, langs)
	if err != nil {
		log.Printf("acquire: subtitles: due searches: %v", err)
		return
	}
	var found, none, failed int
	for i, d := range due {
		if ctx.Err() != nil {
			return
		}
		if i > 0 {
			if sleep(ctx, subtitlePause) != nil {
				return
			}
		}
		f, n, err := s.searchSubtitles(ctx, d, set)
		found, none = found+f, none+n
		if err != nil {
			failed++
			log.Printf("acquire: subtitles: %s: %v", d.ItemID, err)
			var se *katalog.SeamError
			if errors.As(err, &se) && se.NoSeam() {
				s.subtitleProblem("the catalog can't take subtitles yet (POST /api/library/items/{id}/subtitles): update katalog-manager")
				break
			}
			continue
		}
		if f > 0 {
			downloaded += s.spendSubtitleBudget(ctx, set, langs)
		}
	}
	if len(due) > 0 || downloaded > 0 {
		log.Printf("acquire: subtitles: %d titles searched — %d languages found, %d not found, %d failed; %d downloaded",
			len(due), found, none, failed, downloaded)
		s.notify()
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// BudgetLeft is how many automatic downloads the day (UTC) has left: the
// budget less what the automatic work downloaded today, never more than the
// account says it has left. A person's picks draw on the account alone
// (AccountLeft): the account's downloads beyond the budget stay for them.
func (s *Service) BudgetLeft(ctx context.Context) (left, used int) {
	used, err := s.st.SubtitleDownloadsSince(ctx, dayStart(time.Now()), "auto")
	if err != nil {
		return 0, 0
	}
	left = s.cfg.SubtitleDailyBudget - used
	if q := s.subs.client.LastQuota(); q.Known && q.Remaining < left && quotaOfToday(q, time.Now()) {
		left = q.Remaining
	}
	if left < 0 {
		left = 0
	}
	return left, used
}

// AccountLeft is how many downloads the account has left today: the one
// number the console shows, and what a person's pick takes from. false when
// it is not known for today; with ask, the account is asked then (asking
// spends no download).
func (s *Service) AccountLeft(ctx context.Context, ask bool) (int, bool) {
	q := s.subs.client.LastQuota()
	if ask && (!q.Known || !quotaOfToday(q, time.Now())) {
		if _, fresh, err := s.subs.client.Account(ctx); err == nil {
			q = fresh
		}
	}
	if !q.Known || !quotaOfToday(q, time.Now()) {
		return 0, false
	}
	return max(q.Remaining, 0), true
}

// quotaOfToday reports whether what the account said of its downloads still
// holds: before the reset it named, or, when it named none, said today (UTC).
func quotaOfToday(q opensubtitles.Quota, now time.Time) bool {
	if !q.ResetAt.IsZero() {
		return now.Before(q.ResetAt.Add(time.Minute))
	}
	return !q.At.Before(dayStart(now))
}

func dayStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// spendSubtitleBudget downloads the matches in line while the day's budget
// lasts — with downloading on its own off, those a person asked for — and
// answers how many it attached. The catalog is asked of each title first: no
// download goes to a title that has the language by now, or takes no
// subtitle. The quota run out, or a catalog or source that does not answer,
// ends the spending until the next pass.
func (s *Service) spendSubtitleBudget(ctx context.Context, set SubtitleSettings, langs []string) int {
	left, _ := s.BudgetLeft(ctx)
	if left <= 0 {
		return 0
	}
	line, err := s.st.FoundSubtitles(ctx, left, !set.Auto)
	if err != nil {
		log.Printf("acquire: subtitles: the found matches: %v", err)
		return 0
	}
	n := 0
	for _, m := range line {
		k := store.SubtitleKey{ItemID: m.ItemID, Language: m.Want.Language}
		var c subtitles.Candidate
		if json.Unmarshal(m.Want.Found, &c) != nil || c.FileID == 0 {
			_ = s.st.RecordSubtitleSearch(ctx, k, store.SubtitleSearchOutcome{State: store.SubtitleWanted,
				Trouble: "the last result couldn't be read", Detail: "the last result couldn't be read; searched again"})
			continue
		}
		cat, t, err := s.readSubtitleCatalog(ctx, m.SubtitleTitle, langs)
		if s.forgetIfGone(ctx, m.ItemID, err) {
			continue
		}
		if err != nil {
			log.Printf("acquire: subtitles: %s: no download while the catalog does not answer: %v", m.ItemID, err)
			return n
		}
		if _, had := s.hasSubtitles(cat, langs)[k.Language]; had || !t.Attachable {
			continue
		}
		err = s.attachCandidate(ctx, t, k.Language, c, "auto")
		var qe *opensubtitles.QuotaError
		var down *unavailableError
		switch {
		case errors.As(err, &qe), errors.As(err, &down):
			log.Printf("acquire: subtitles: %v", err)
			return n
		case err == nil:
			n++
		}
	}
	return n
}

// forgetIfGone forgets the title when err is the catalog saying it holds no
// such title — nothing to attach to, ever — and reports whether it did.
func (s *Service) forgetIfGone(ctx context.Context, itemID string, err error) bool {
	var se *katalog.SeamError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound || se.NoSeam() {
		return false
	}
	if _, ferr := s.st.ForgetSubtitleTitle(ctx, itemID); ferr != nil {
		log.Printf("acquire: subtitles: forget %s: %v", itemID, ferr)
	}
	return true
}

// readSubtitleCatalog asks the catalog what the title has and records it: the
// version a subtitle is attached to, the languages it has a full subtitle in,
// and its original's fingerprint, read once.
func (s *Service) readSubtitleCatalog(ctx context.Context, t store.SubtitleTitle, langs []string) (katalog.ItemSubtitles, store.SubtitleTitle, error) {
	fingerprint := t.Sum64 == "" && t.OriginalNote == ""
	cat, err := s.kc.ItemSubtitles(ctx, t.ItemID, fingerprint)
	if err != nil {
		return katalog.ItemSubtitles{}, t, err
	}
	has := s.hasSubtitles(cat, langs)
	rec := store.SubtitleCatalog{Attachable: cat.Attachable, NotAttachable: cat.Reason, Has: has,
		Other: otherScripts(cat, langs, has), Languages: langs}
	if rec.Languages == nil {
		rec.Languages = []string{}
	}
	if cat.VersionID != nil {
		rec.VersionID = *cat.VersionID
	}
	if fingerprint {
		fp := &store.SubtitleFingerprint{Note: cat.OriginalNote, Labels: map[string]string{}}
		if o := cat.Original; o != nil {
			fp.SizeBytes, fp.Sum64 = o.SizeBytes, o.Sum64
			for k, v := range o.Labels {
				if str, ok := v.(string); ok && str != "" {
					fp.Labels[k] = str
				}
			}
		} else if fp.Note == "" {
			fp.Note = "the catalog gave no fingerprint"
		}
		rec.Fingerprint = fp
		t.SizeBytes, t.Sum64, t.Labels, t.OriginalNote = fp.SizeBytes, fp.Sum64, fp.Labels, fp.Note
	}
	if err := s.st.SetSubtitleCatalog(ctx, t.ItemID, rec); err != nil {
		return katalog.ItemSubtitles{}, t, err
	}
	t.Attachable, t.NotAttachable, t.VersionID = rec.Attachable, rec.NotAttachable, rec.VersionID
	return cat, t, nil
}

// hasSubtitles is how the title has each of langs, of its full subtitles (a
// forced one is none): its own, one attached, one a model made (ai), or, for
// Chinese that names no script, one in Traditional characters (traditional).
// Chinese that names a script is had in that script alone (subtitles.Serves).
func (s *Service) hasSubtitles(cat katalog.ItemSubtitles, langs []string) map[string]string {
	out := map[string]string{}
	for _, l := range langs {
		for _, sub := range cat.Subtitles {
			if sub.Forced {
				continue
			}
			ok, how := subtitles.Serves(sub.Language, sub.LabelText(), l)
			if !ok {
				continue
			}
			kind := "own"
			switch {
			case how == "hant":
				kind = "traditional"
			case sub.Origin != nil && (*sub.Origin == "transcribed" || *sub.Origin == "translated"):
				kind = "ai" // made by a model, as its label says ("German · AI")
			case sub.Attached:
				kind = "attached"
			}
			if prev, seen := out[l]; !seen || prev == "traditional" {
				out[l] = kind
			}
		}
	}
	return out
}

// otherScripts is, of langs that name a Chinese script and the title has no
// full subtitle in, the script ("hant" or "hans") of one it has in the other
// characters: the row says so, as that one doesn't count for the language.
func otherScripts(cat katalog.ItemSubtitles, langs []string, has map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range langs {
		if _, ok := has[l]; ok {
			continue
		}
		for _, sub := range cat.Subtitles {
			if other := subtitles.OtherScript(sub.Language, sub.LabelText(), l); other != "" && !sub.Forced {
				out[l] = other
				break
			}
		}
	}
	return out
}

// subtitleTarget is what ranking knows of the title's file.
func subtitleTarget(t store.SubtitleTitle) subtitles.Target {
	tg := subtitles.Target{Kind: t.Kind, Title: t.Title, Season: t.Season, Episode: t.Episode, Labels: t.Labels, Hash: t.Sum64 != "",
		IMDbID: subtitles.IMDbNumber(t.IMDbID), ParentIMDbID: subtitles.IMDbNumber(t.ParentIMDbID)}
	if t.Release != "" {
		tg.Releases = []string{t.Release}
	}
	return tg
}

// subtitleQuery is the search for the title in the source's codes of langs.
func subtitleQuery(t store.SubtitleTitle, langs []string) opensubtitles.Query {
	q := opensubtitles.Query{MovieHash: t.Sum64, Type: t.Kind}
	for _, l := range langs {
		q.Languages = append(q.Languages, subtitles.SourceCodes(l)...)
	}
	if t.Kind == "episode" {
		q.ParentIMDbID, q.ParentTMDBID, q.Season, q.Episode = t.ParentIMDbID, t.ParentTMDBID, t.Season, t.Episode
		if q.ParentIMDbID == "" && q.ParentTMDBID == 0 {
			q.Text = t.Title
		}
	} else {
		q.IMDbID, q.TMDBID = t.IMDbID, t.TMDBID
		if q.IMDbID == "" && q.TMDBID == 0 {
			q.Text, q.Year = t.Title, t.Year
		}
	}
	return q
}

// searchResults runs the search, two pages at most.
func (s *Service) searchResults(ctx context.Context, q opensubtitles.Query) ([]opensubtitles.Result, error) {
	var out []opensubtitles.Result
	for page := 1; page <= 2; page++ {
		q.Page = page
		p, err := s.subs.client.Search(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Results...)
		if p.TotalPages <= page {
			break
		}
	}
	return out, nil
}

// rankFor ranks the results for one language, leaving out the files a
// person removed for it.
func rankFor(results []opensubtitles.Result, lang string, t store.SubtitleTitle, blocked []int64, set SubtitleSettings) []subtitles.Candidate {
	ranked := subtitles.Rank(results, lang, subtitleTarget(t), subtitles.Options{AllowMachineTranslated: set.AllowMachineTranslated})
	out := ranked[:0]
	for _, c := range ranked {
		if slices.Contains(blocked, c.FileID) {
			continue
		}
		out = append(out, c)
	}
	if len(out) > 0 && !out[0].Best && out[0].Excluded == "" {
		out[0].Best = true // the first was a removed one
	}
	return out
}

// searchSubtitles looks for the due languages of one title: the catalog asked
// what it has, then one search for the instance's languages it has not that
// are due — those listed due, and those the catalog's answer made wanted (a
// language the library lost); each is found (its best match kept), or not
// found, looked for again after days. It answers how many were found and
// not found.
func (s *Service) searchSubtitles(ctx context.Context, d store.DueSubtitle, set SubtitleSettings) (int, int, error) {
	langs, _, err := s.SubtitleLanguages(ctx)
	if err != nil {
		return 0, 0, err
	}
	cat, t, err := s.readSubtitleCatalog(ctx, d.SubtitleTitle, langs)
	if s.forgetIfGone(ctx, d.ItemID, err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if !t.Attachable {
		return 0, 0, nil // not packaged in the library: its packaged event brings it back
	}
	has := s.hasSubtitles(cat, langs)
	wants, err := s.st.SubtitleWantsOf(ctx, t.ItemID)
	if err != nil {
		return 0, 0, err
	}
	now := time.Now()
	var want []string
	of := map[string]store.SubtitleWant{}
	for _, w := range wants {
		_, had := has[w.Language]
		due := slices.Contains(d.Languages, w.Language) ||
			(w.State == store.SubtitleWanted && (w.NextAt == nil || !w.NextAt.After(now)))
		if !had && due && slices.Contains(langs, w.Language) {
			want, of[w.Language] = append(want, w.Language), w
		}
	}
	if len(want) == 0 {
		return 0, 0, nil
	}
	results, err := s.searchResults(ctx, subtitleQuery(t, want))
	if err != nil {
		next := time.Now().Add(subtitleFailWait)
		for _, l := range want {
			_ = s.st.RecordSubtitleSearch(ctx, store.SubtitleKey{ItemID: t.ItemID, Language: l}, store.SubtitleSearchOutcome{
				State: store.SubtitleWanted, Trouble: "the search failed", Detail: "the search failed: " + err.Error(), Next: &next})
		}
		return 0, 0, err
	}
	found, none := 0, 0
	for _, l := range want {
		k := store.SubtitleKey{ItemID: t.ItemID, Language: l}
		w := of[l]
		ranked := rankFor(results, l, t, w.Blocked, set)
		if len(ranked) > 0 && ranked[0].Best {
			found++
			_ = s.st.RecordSubtitleSearch(ctx, k, store.SubtitleSearchOutcome{State: store.SubtitleFound, Found: ranked[0],
				Versions: usable(ranked), Detail: foundDetail(ranked)})
			continue
		}
		none++
		next := time.Now().Add(notFoundWait(w.Searches))
		trouble, detail := notFoundWords(ranked)
		_ = s.st.RecordSubtitleSearch(ctx, k, store.SubtitleSearchOutcome{State: store.SubtitleNotFound, Trouble: trouble,
			Detail: detail, Next: &next})
	}
	return found, none, nil
}

// notFoundWait is the wait after the searches-th search that found nothing:
// a week, doubling, at most four.
func notFoundWait(searches int) time.Duration {
	w := time.Duration(float64(subtitleNotFoundWait) * math.Pow(2, float64(searches)))
	if w > subtitleNotFoundMax || w <= 0 {
		return subtitleNotFoundMax
	}
	return w
}

// usable is how many of the ranked an automatic choice may take.
func usable(ranked []subtitles.Candidate) int {
	n := 0
	for _, c := range ranked {
		if c.Excluded == "" {
			n++
		}
	}
	return n
}

// notFoundWords is why nothing was taken, in plain words for the console's
// line and in the source's terms for its details: nothing at all, only
// machine translations, or nothing usable for this title.
func notFoundWords(ranked []subtitles.Candidate) (trouble, detail string) {
	detail = notFoundDetail(ranked)
	if len(ranked) == 0 {
		return "nothing found", detail
	}
	machine := 0
	for _, c := range ranked {
		if c.Excluded == "machine-translated" || c.Excluded == "AI-translated" {
			machine++
		}
	}
	if machine == len(ranked) {
		return "only machine translations", detail
	}
	return "nothing usable found", detail
}

// foundDetail says what a search found, in words: how many, and how the best
// fits.
func foundDetail(ranked []subtitles.Candidate) string {
	best := ranked[0]
	n := 0
	for _, c := range ranked {
		if c.Excluded == "" {
			n++
		}
	}
	how := map[string]string{subtitles.MatchFile: "made for this file", subtitles.MatchRelease: "made for this release",
		subtitles.MatchUnknown: "may be out of sync"}[best.Match]
	line := fmt.Sprintf("%d found; the best %s", n, how)
	if best.Traditional {
		line += ", in Traditional Chinese"
	}
	return line
}

// notFoundDetail says why nothing was taken: nothing at all, or only what an
// automatic choice never takes.
func notFoundDetail(ranked []subtitles.Candidate) string {
	if len(ranked) == 0 {
		return "nothing in this language"
	}
	reasons := map[string]int{}
	for _, c := range ranked {
		reasons[c.Excluded]++
	}
	var parts []string
	for _, why := range []string{"machine-translated", "AI-translated", "foreign parts only"} {
		if reasons[why] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", reasons[why], why))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d found, none for this title", len(ranked))
	}
	return "only " + strings.Join(parts, ", ") + " — never downloaded automatically"
}

// unavailableError is a failure of the catalog or the source themselves,
// not of the file: the want is left as it was, and no more downloads are
// spent until they answer.
type unavailableError struct{ error }

func (e *unavailableError) Unwrap() error { return e.error }

// attachCandidate downloads the candidate, attaches it to the title through
// the catalog (origin provider), checks the catalog lists it, and records it
// as had. A failure of the file — the source refusing it, its text the
// catalog refuses, one it cannot read — is recorded on the want with the file
// never taken again for it: the next try takes the next version, or, with no
// other usable one left, waits for a new one as for a title nothing was
// found for. A failure that may pass when tried again is recorded too, the
// same version tried again. Each says what went wrong in plain words. A
// QuotaError, an unavailableError, and a title the catalog says takes no
// subtitle now (recorded on the title) leave the want as it was.
func (s *Service) attachCandidate(ctx context.Context, t store.SubtitleTitle, lang string, c subtitles.Candidate, how string) error {
	k := store.SubtitleKey{ItemID: t.ItemID, Language: lang}
	fail := func(trouble string, err error, theFile bool) error {
		f := store.SubtitleFailure{Trouble: trouble, Detail: err.Error(), Retry: store.SubtitleRetrySame}
		next := time.Now().Add(subtitleFailWait)
		if theFile {
			_ = s.st.BlockSubtitleFile(ctx, k, c.FileID)
			f.Retry = store.SubtitleRetryAnother
			// the last search found this one usable version alone: none is left
			if w, werr := s.st.SubtitleWantOf(ctx, k); werr == nil && w.Searches > 0 && w.Versions <= 1 {
				f.Retry, next = store.SubtitleRetryNone, time.Now().Add(notFoundWait(w.Searches))
			}
		}
		f.Next = &next
		_ = s.st.RecordSubtitleFailed(ctx, k, f)
		return err
	}
	link, err := s.subs.client.Download(ctx, c.FileID)
	d := store.SubtitleDownload{ItemID: t.ItemID, Language: lang, FileID: c.FileID, How: how, OK: err == nil}
	if err == nil {
		r := link.Quota.Remaining
		d.Remaining = &r
	} else {
		d.Detail = err.Error()
	}
	_ = s.st.RecordSubtitleDownload(ctx, d)
	var qe *opensubtitles.QuotaError
	var oe *opensubtitles.Error
	switch {
	case errors.As(err, &qe):
		return err
	case errors.As(err, &oe) && oe.Status >= 400 && oe.Status < 500 && !sourceRefusesAcquire(oe.Status):
		return fail("the source refused the file", fmt.Errorf("the download was refused: %w", err), true) // gone, or not to be had
	case err != nil:
		return &unavailableError{fmt.Errorf("the download was refused: %w", err)}
	}
	body, err := s.subs.client.Fetch(ctx, link.URL)
	var fe *opensubtitles.FileError
	switch {
	case errors.As(err, &fe):
		return fail("the file can't be used", err, true)
	case errors.As(err, &oe) && oe.Status >= 400 && oe.Status < 500:
		return fail("the file's link didn't work", err, false)
	case err != nil:
		return &unavailableError{err}
	}
	text, err := subtitles.UTF8(body, c.Language)
	if err != nil {
		return fail("can't read the file", err, true)
	}
	res, err := s.kc.AttachSubtitle(ctx, t.ItemID, katalog.AttachRequest{Language: c.Tag, Forced: false,
		SDH: c.HearingImpaired, Origin: "provider", Content: string(text)})
	var se *katalog.SeamError
	switch {
	case err == nil:
	case errors.As(err, &se) && (se.Code == http.StatusBadRequest || se.Code == http.StatusRequestEntityTooLarge ||
		se.Code == http.StatusUnprocessableEntity):
		return fail("the catalog refused it", fmt.Errorf("the catalog did not take it: %w", err), true) // what the file holds
	case errors.As(err, &se) && se.Code == http.StatusConflict:
		_ = s.st.SubtitleTitleNotAttachable(ctx, t.ItemID, se.Message())
		return fmt.Errorf("the catalog did not take it: %w", err)
	case s.forgetIfGone(ctx, t.ItemID, err):
		return fmt.Errorf("the catalog did not take it: %w", err)
	default:
		return &unavailableError{fmt.Errorf("the catalog did not take it: %w", err)}
	}
	// what a player lists: the catalog's subtitles of the title; when it
	// cannot be asked, its answer to the attach stands until the next read
	if cat, err := s.kc.ItemSubtitles(ctx, res.ItemID, false); err != nil {
		log.Printf("acquire: subtitles: %s %s: the catalog took it, and could not be asked whether it lists it: %v", t.ItemID, lang, err)
	} else if !slices.ContainsFunc(cat.Subtitles, func(sub katalog.CatalogSubtitle) bool { return sub.ID == res.Subtitle.ID }) {
		return fail("the catalog doesn't list it", errors.New("the catalog took it, and does not list it"), false)
	}
	kind := "attached"
	switch {
	case c.Traditional:
		kind = "traditional"
	case c.AITranslated || c.MachineTranslated:
		kind = "ai"
	}
	if err := s.st.RecordSubtitleAttached(ctx, k, store.SubtitleAttached{SubtitleID: res.Subtitle.ID, FileID: c.FileID,
		Release: c.Release, Match: c.Match, Preview: preview(text, subtitlePreviewCues), How: how, Kind: kind}); err != nil {
		return err
	}
	s.subtitleProblem("") // the account and the catalog work
	log.Printf("acquire: subtitles: %s %s attached (%s, %s)", t.ItemID, lang, c.Match, how)
	return nil
}

// sourceRefusesAcquire reports whether a status of the source is about the
// account, its key or its pace (401, 403, 429), not about a file.
func sourceRefusesAcquire(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests
}

// subtitleRemoved stops looking at a title the catalog removed.
func (s *Service) subtitleRemoved(ctx context.Context, itemID string) {
	if s.subs == nil || itemID == "" {
		return
	}
	if gone, err := s.st.ForgetSubtitleTitle(ctx, itemID); err != nil {
		log.Printf("acquire: subtitles: removed %s: %v", itemID, err)
	} else if gone {
		s.notify()
	}
}

// preview is the first n cues of a SubRip or WebVTT text, each its time and
// its lines.
func preview(text []byte, n int) string {
	var out []string
	blocks := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n\n")
	for _, b := range blocks {
		lines := strings.Split(strings.TrimSpace(b), "\n")
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				cue := strings.TrimSpace(strings.SplitN(l, "-->", 2)[0])
				if dot := strings.IndexAny(cue, ",."); dot > 0 {
					cue = cue[:dot]
				}
				body := strings.Join(lines[i+1:], " / ")
				if body != "" {
					out = append(out, cue+"  "+body)
				}
				break
			}
		}
		if len(out) >= n {
			break
		}
	}
	return strings.Join(out, "\n")
}

// subtitleArrival takes a packaged movie or episode on as a new arrival: its
// identities from the event, its names from acquire's targets (else the
// catalog's), the catalog asked again, every language it has not due now.
func (s *Service) subtitleArrival(ctx context.Context, ev events.ItemEvent) {
	if !s.subtitlesOn() || (ev.Type != "movie" && ev.Type != "episode") {
		return
	}
	t, ok, err := s.st.SubtitleTitleOfTarget(ctx, ev.ItemID)
	if err != nil {
		log.Printf("acquire: subtitles: arrival %s: %v", ev.ItemID, err)
		return
	}
	if !ok {
		t = store.SubtitleTitle{ItemID: ev.ItemID, Kind: ev.Type}
		if ev.Type == "movie" {
			t.IMDbID, t.TMDBID = ev.ExternalIDs.IMDb(), ev.ExternalIDs.Int("tmdb")
		} else {
			t.ParentIMDbID, t.ParentTMDBID = ev.SeriesExternalIDs.IMDb(), ev.SeriesExternalIDs.Int("tmdb")
			if ev.SeasonNumber != nil && ev.EpisodeNumber != nil {
				t.Season, t.Episode = *ev.SeasonNumber, *ev.EpisodeNumber
			}
		}
		s.nameFromCatalog(ctx, &t)
	}
	now := time.Now()
	t.Priority, t.PackagedAt = store.SubtitleNewArrival, &now
	langs, _, _ := s.SubtitleLanguages(ctx)
	if err := s.st.PutSubtitleTitle(ctx, t, langs); err != nil {
		log.Printf("acquire: subtitles: arrival %s: %v", ev.ItemID, err)
		return
	}
	if err := s.st.RenewSubtitleTitle(ctx, ev.ItemID); err != nil {
		log.Printf("acquire: subtitles: arrival %s: %v", ev.ItemID, err)
	}
	s.wakeSubtitles()
}

// nameFromCatalog names a title acquire tracks no target for, as the catalog
// does: a movie's title and year; an episode's series' title and its numbers.
func (s *Service) nameFromCatalog(ctx context.Context, t *store.SubtitleTitle) {
	it, err := s.kc.ItemName(ctx, t.ItemID)
	if err != nil {
		return
	}
	if it.Year != nil {
		t.Year = *it.Year
	}
	if t.Kind == "movie" {
		t.Title = it.Title
		return
	}
	if it.Season != nil && it.Episode != nil && t.Season == 0 && t.Episode == 0 {
		t.Season, t.Episode = *it.Season, *it.Episode
	}
	parent := it.ParentID
	for i := 0; i < 2 && parent != ""; i++ { // a season's parent is its series
		p, err := s.kc.ItemName(ctx, parent)
		if err != nil {
			return
		}
		if p.Type == "series" {
			t.Title = p.Title
			return
		}
		parent = p.ParentID
	}
}
