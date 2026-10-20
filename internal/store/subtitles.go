package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Subtitle want states (subtitle_wants.state, migration 080).
const (
	// SubtitleHas: the title has a full subtitle in the language, its own or
	// one attached to it.
	SubtitleHas = "has"
	// SubtitleWanted: not searched yet, or due again.
	SubtitleWanted = "wanted"
	// SubtitleFound: a match was found and waits for a download.
	SubtitleFound = "found"
	// SubtitleNotFound: nothing usable was found; looked for again at next_at.
	SubtitleNotFound = "notfound"
	// SubtitleFailed: a download or the attach failed; it needs attention.
	SubtitleFailed = "failed"
)

// Subtitle priorities, the most urgent first.
const (
	SubtitleNewArrival = 0
	SubtitleAskedFor   = 1
	SubtitleLibrary    = 2
)

// SubtitleTitle is a catalog item acquire looks subtitles for: the item whose
// file it is, what it is, the identities a subtitle source is asked by, and
// what the catalog last said of it.
type SubtitleTitle struct {
	ItemID        string            `json:"itemId"`
	Kind          string            `json:"kind"`
	Title         string            `json:"title"`
	Year          int               `json:"year"`
	Season        int               `json:"season"`
	Episode       int               `json:"episode"`
	IMDbID        string            `json:"imdbId"`
	TMDBID        int64             `json:"tmdbId"`
	ParentIMDbID  string            `json:"parentImdbId"`
	ParentTMDBID  int64             `json:"parentTmdbId"`
	TargetID      string            `json:"targetId"`
	TitleID       string            `json:"titleId"`
	Release       string            `json:"release"`
	Priority      int               `json:"priority"`
	AiredAt       *time.Time        `json:"airedAt"`
	PackagedAt    *time.Time        `json:"packagedAt"`
	VersionID     string            `json:"versionId"`
	Attachable    bool              `json:"attachable"`
	NotAttachable string            `json:"notAttachable"`
	SizeBytes     int64             `json:"sizeBytes"`
	Sum64         string            `json:"sum64"`
	Labels        map[string]string `json:"labels"`
	OriginalNote  string            `json:"originalNote"`
	ReadAt        *time.Time        `json:"readAt"`
}

const subtitleTitleCols = `t.item_id, t.kind, t.title, COALESCE(t.year, 0), COALESCE(t.season, 0), COALESCE(t.episode, 0),
	t.imdb_id, COALESCE(t.tmdb_id, 0), t.parent_imdb_id, COALESCE(t.parent_tmdb_id, 0), t.target_id, t.title_id, t.release,
	t.priority, t.aired_at, t.packaged_at, t.version_id, t.attachable, t.not_attachable, COALESCE(t.size_bytes, 0), t.sum64,
	t.labels, t.original_note, t.read_at`

func (t *SubtitleTitle) dest() []any {
	return []any{&t.ItemID, &t.Kind, &t.Title, &t.Year, &t.Season, &t.Episode, &t.IMDbID, &t.TMDBID, &t.ParentIMDbID,
		&t.ParentTMDBID, &t.TargetID, &t.TitleID, &t.Release, &t.Priority, &t.AiredAt, &t.PackagedAt, &t.VersionID,
		&t.Attachable, &t.NotAttachable, &t.SizeBytes, &t.Sum64, &t.Labels, &t.OriginalNote, &t.ReadAt}
}

// SubtitleWant is one of a title's instance languages and what acquire did
// about it.
type SubtitleWant struct {
	ItemID   string `json:"itemId"`
	Language string `json:"language"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
	Trouble  string `json:"trouble"`  // what keeps the language from the title, in plain words
	Retry    string `json:"retry"`    // a failed want's next try: SubtitleRetryAnother, -Same, -None
	Versions int    `json:"versions"` // the usable versions the last search found
	// OtherScript is the script ("hant", "hans") of a subtitle the title has
	// in the other characters than the language names, which doesn't count.
	OtherScript string          `json:"otherScript,omitempty"`
	HasKind     string          `json:"hasKind"`
	Found       json.RawMessage `json:"found,omitempty"`
	SearchedAt  *time.Time      `json:"searchedAt"`
	NextAt      *time.Time      `json:"nextAt"`
	Searches    int             `json:"searches"`
	Failures    int             `json:"failures"`
	AskedAt     *time.Time      `json:"askedAt"`
	SubtitleID  string          `json:"subtitleId"`
	FileID      int64           `json:"fileId"`
	Release     string          `json:"release"`
	Match       string          `json:"match"`
	Preview     string          `json:"preview,omitempty"`
	AttachedAt  *time.Time      `json:"attachedAt"`
	AttachedHow string          `json:"attachedHow"`
	Blocked     []int64         `json:"blocked"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

const subtitleWantCols = `w.item_id, w.language, w.state, w.detail, w.trouble, w.retry, w.versions, w.other_script, w.has_kind, w.found,
	w.searched_at, w.next_at, w.searches, w.failures, w.asked_at, w.subtitle_id, COALESCE(w.file_id, 0), w.release, w.match,
	w.preview, w.attached_at, w.attached_how, w.blocked, w.updated_at`

func (w *SubtitleWant) dest() []any {
	return []any{&w.ItemID, &w.Language, &w.State, &w.Detail, &w.Trouble, &w.Retry, &w.Versions, &w.OtherScript, &w.HasKind, &w.Found,
		&w.SearchedAt, &w.NextAt, &w.Searches, &w.Failures, &w.AskedAt, &w.SubtitleID, &w.FileID, &w.Release, &w.Match,
		&w.Preview, &w.AttachedAt, &w.AttachedHow, &w.Blocked, &w.UpdatedAt}
}

// What a failed want does next (subtitle_wants.retry).
const (
	// SubtitleRetryAnother: the next version; the one that failed is blocked.
	SubtitleRetryAnother = "another"
	// SubtitleRetrySame: the same version again; what failed was not the file.
	SubtitleRetrySame = "same"
	// SubtitleRetryNone: no other version yet; looked for again after days.
	SubtitleRetryNone = "none"
)

// SubtitleKey names one want: a title and a language.
type SubtitleKey struct {
	ItemID   string `json:"itemId"`
	Language string `json:"language"`
}

// PutSubtitleTitle records a title acquire looks subtitles for, or what is
// new of one it records: identities and names it did not have, the most
// urgent priority of the two, the newest packaging. It has a want for each of
// langs, and none for another language — but for one acquire attached a
// subtitle in, which stays removable; with no langs (the languages unknown
// for now) its wants stay as they are.
func (s *Store) PutSubtitleTitle(ctx context.Context, t SubtitleTitle, langs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO subtitle_titles (item_id, kind, title, year, season, episode, imdb_id, tmdb_id, parent_imdb_id,
			parent_tmdb_id, target_id, title_id, release, priority, aired_at, packaged_at)
		VALUES ($1, $2, $3, NULLIF($4, 0), NULLIF($5, 0), NULLIF($6, 0), $7, NULLIF($8, 0), $9, NULLIF($10, 0), $11, $12,
			$13, $14, $15, $16)
		ON CONFLICT (item_id) DO UPDATE SET
			kind = EXCLUDED.kind,
			title = COALESCE(NULLIF(EXCLUDED.title, ''), subtitle_titles.title),
			year = COALESCE(EXCLUDED.year, subtitle_titles.year),
			season = COALESCE(EXCLUDED.season, subtitle_titles.season),
			episode = COALESCE(EXCLUDED.episode, subtitle_titles.episode),
			imdb_id = COALESCE(NULLIF(EXCLUDED.imdb_id, ''), subtitle_titles.imdb_id),
			tmdb_id = COALESCE(EXCLUDED.tmdb_id, subtitle_titles.tmdb_id),
			parent_imdb_id = COALESCE(NULLIF(EXCLUDED.parent_imdb_id, ''), subtitle_titles.parent_imdb_id),
			parent_tmdb_id = COALESCE(EXCLUDED.parent_tmdb_id, subtitle_titles.parent_tmdb_id),
			target_id = COALESCE(NULLIF(EXCLUDED.target_id, ''), subtitle_titles.target_id),
			title_id = COALESCE(NULLIF(EXCLUDED.title_id, ''), subtitle_titles.title_id),
			release = COALESCE(NULLIF(EXCLUDED.release, ''), subtitle_titles.release),
			priority = LEAST(EXCLUDED.priority, subtitle_titles.priority),
			aired_at = COALESCE(EXCLUDED.aired_at, subtitle_titles.aired_at),
			packaged_at = GREATEST(EXCLUDED.packaged_at, subtitle_titles.packaged_at),
			updated_at = now()`,
		t.ItemID, t.Kind, t.Title, t.Year, t.Season, t.Episode, t.IMDbID, t.TMDBID, t.ParentIMDbID, t.ParentTMDBID,
		t.TargetID, t.TitleID, t.Release, t.Priority, t.AiredAt, t.PackagedAt); err != nil {
		return err
	}
	if len(langs) > 0 { // the languages unknown for now: the wants stay as they are
		if err := ensureWants(ctx, tx, t.ItemID, langs); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ForgetSubtitleTitle stops looking at a title the catalog no longer holds:
// its row and its wants go.
func (s *Store) ForgetSubtitleTitle(ctx context.Context, itemID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM subtitle_titles WHERE item_id = $1`, itemID)
	return tag.RowsAffected() > 0, err
}

// ensureWants gives the title a want for each of langs, and takes away the
// others but for one holding a subtitle acquire attached.
func ensureWants(ctx context.Context, q pgx.Tx, itemID string, langs []string) error {
	if langs == nil {
		langs = []string{}
	}
	if _, err := q.Exec(ctx, `INSERT INTO subtitle_wants (item_id, language) SELECT $1, l FROM unnest($2::text[]) AS l
		ON CONFLICT DO NOTHING`, itemID, langs); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM subtitle_wants WHERE item_id = $1 AND NOT (language = ANY($2)) AND subtitle_id = ''`,
		itemID, langs)
	return err
}

// EnsureSubtitleWants gives every title a want for each of langs and takes
// away the others (but for one holding a subtitle acquire attached): the
// instance's languages changed.
func (s *Store) EnsureSubtitleWants(ctx context.Context, langs []string) error {
	if langs == nil {
		langs = []string{}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO subtitle_wants (item_id, language)
		SELECT t.item_id, l FROM subtitle_titles t CROSS JOIN unnest($1::text[]) AS l ON CONFLICT DO NOTHING`, langs); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM subtitle_wants WHERE NOT (language = ANY($1)) AND subtitle_id = ''`, langs)
	return err
}

// FillSubtitleTitles records, from the targets acquire tracks, up to limit
// titles the library holds and plays that acquire looks no subtitles for
// yet, the newest first: a movie, or an episode with a file of its own (one
// covered by another's has its holder's), with the release name known of its
// file. A title acquire grabbed, or someone requested, is asked for; the rest
// is the library's. It answers how many it recorded.
func (s *Store) FillSubtitleTitles(ctx context.Context, limit int, langs []string) (int, error) {
	if langs == nil {
		langs = []string{}
	}
	var n int
	err := s.pool.QueryRow(ctx, `
		WITH cand AS (
			SELECT DISTINCT ON (a.held_item_id) a.held_item_id AS item_id, a.kind, t.title, t.year, a.season_number,
				a.episode_number, t.imdb_id AS t_imdb, t.tmdb_id AS t_tmdb, a.id AS target_id, t.id AS title_id,
				a.held_release, a.held_source, a.air_date
			FROM acquisition_targets a JOIN titles t ON t.id = a.title_id
			WHERE a.state = 'held' AND a.held_playable AND a.held_item_id <> '' AND a.held_via = ''
			  AND NOT EXISTS (SELECT 1 FROM subtitle_titles s WHERE s.item_id = a.held_item_id)
			ORDER BY a.held_item_id, a.air_date DESC NULLS LAST
		), ins AS (
			INSERT INTO subtitle_titles (item_id, kind, title, year, season, episode, imdb_id, tmdb_id, parent_imdb_id,
				parent_tmdb_id, target_id, title_id, release, priority, aired_at)
			SELECT c.item_id, c.kind, c.title, c.year, c.season_number, c.episode_number,
				CASE WHEN c.kind = 'movie' THEN c.t_imdb ELSE '' END, CASE WHEN c.kind = 'movie' THEN c.t_tmdb END,
				CASE WHEN c.kind = 'episode' THEN c.t_imdb ELSE '' END, CASE WHEN c.kind = 'episode' THEN c.t_tmdb END,
				c.target_id, c.title_id, c.held_release,
				CASE WHEN c.held_source = 'grab' OR EXISTS (SELECT 1 FROM wanted_items w WHERE w.item_id = c.item_id)
					THEN 1 ELSE 2 END,
				c.air_date
			FROM cand c ORDER BY c.air_date DESC NULLS LAST, c.item_id LIMIT $1
			ON CONFLICT (item_id) DO NOTHING
			RETURNING item_id
		), wants AS (
			INSERT INTO subtitle_wants (item_id, language) SELECT ins.item_id, l FROM ins CROSS JOIN unnest($2::text[]) AS l
			ON CONFLICT DO NOTHING
		)
		SELECT count(*) FROM ins`, limit, langs).Scan(&n)
	return n, err
}

// SubtitleTitleOfTarget is what acquire's targets know of the item whose file
// it is: the target it holds (its own file, not one covered by another's),
// with its title's names and identities, the release name known of the file,
// and whether it was asked for (grabbed or requested). false when no target
// holds it.
func (s *Store) SubtitleTitleOfTarget(ctx context.Context, itemID string) (SubtitleTitle, bool, error) {
	t := SubtitleTitle{ItemID: itemID, Priority: SubtitleLibrary}
	var tImdb, source string
	var tTmdb int64
	err := s.pool.QueryRow(ctx, `SELECT a.kind, ti.title, COALESCE(ti.year, 0), COALESCE(a.season_number, 0),
			COALESCE(a.episode_number, 0), ti.imdb_id, ti.tmdb_id, a.id, ti.id, a.held_release, a.held_source, a.air_date
		FROM acquisition_targets a JOIN titles ti ON ti.id = a.title_id
		WHERE a.held_item_id = $1 AND a.held_via = '' ORDER BY a.air_date DESC NULLS LAST LIMIT 1`, itemID).
		Scan(&t.Kind, &t.Title, &t.Year, &t.Season, &t.Episode, &tImdb, &tTmdb, &t.TargetID, &t.TitleID, &t.Release, &source, &t.AiredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	if t.Kind == "movie" {
		t.IMDbID, t.TMDBID = tImdb, tTmdb
	} else {
		t.ParentIMDbID, t.ParentTMDBID = tImdb, tTmdb
	}
	if source == "grab" {
		t.Priority = SubtitleAskedFor
	}
	return t, true, nil
}

// SubtitleCatalog is what the catalog said of a title's subtitles: the
// version one is attached to, or why there is none; its original's
// fingerprint when it was asked for (Fingerprint), and the languages it has
// a full subtitle in, with how (own, attached, traditional), of the
// languages in use (Languages; nil: of every language the title has a want
// in), and of the others the script of one it has in the other characters
// (Other).
type SubtitleCatalog struct {
	VersionID     string
	Attachable    bool
	NotAttachable string
	Fingerprint   *SubtitleFingerprint
	Has           map[string]string
	Other         map[string]string
	Languages     []string
}

// SubtitleFingerprint is what subtitle sources match a title's original by.
type SubtitleFingerprint struct {
	SizeBytes int64
	Sum64     string
	Labels    map[string]string
	Note      string
}

// SetSubtitleCatalog records what the catalog said of a title: its version
// and the fingerprint, and each want's language it has a full subtitle in is
// had; one had before that it no longer has is wanted again, at once (the
// library lost it: another file, or someone removed it). A want of a
// language that is no longer the instance's (kept for the subtitle acquire
// attached in it) is left as it is: Has does not say of it. Each want of a
// language in use keeps the script of a subtitle the title has in the other
// characters (Other), or none.
func (s *Store) SetSubtitleCatalog(ctx context.Context, itemID string, c SubtitleCatalog) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if c.Fingerprint != nil {
		f := c.Fingerprint
		labels := f.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		_, err = tx.Exec(ctx, `UPDATE subtitle_titles SET version_id = $2, attachable = $3, not_attachable = $4, read_at = now(),
			size_bytes = NULLIF($5, 0), sum64 = $6, labels = $7, original_note = $8, updated_at = now() WHERE item_id = $1`,
			itemID, c.VersionID, c.Attachable, c.NotAttachable, f.SizeBytes, f.Sum64, labels, f.Note)
	} else {
		_, err = tx.Exec(ctx, `UPDATE subtitle_titles SET version_id = $2, attachable = $3, not_attachable = $4, read_at = now(),
			updated_at = now() WHERE item_id = $1`, itemID, c.VersionID, c.Attachable, c.NotAttachable)
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT language, state FROM subtitle_wants WHERE item_id = $1 FOR UPDATE`, itemID)
	if err != nil {
		return err
	}
	type want struct{ lang, state string }
	var wants []want
	for rows.Next() {
		var w want
		if err := rows.Scan(&w.lang, &w.state); err != nil {
			rows.Close()
			return err
		}
		wants = append(wants, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, w := range wants {
		if c.Languages != nil && !slices.Contains(c.Languages, w.lang) {
			continue
		}
		kind, has := c.Has[w.lang]
		switch {
		case has:
			_, err = tx.Exec(ctx, `UPDATE subtitle_wants SET state = 'has', has_kind = $3, found = NULL, next_at = NULL,
				asked_at = NULL, failures = 0, detail = CASE WHEN state = 'has' THEN detail ELSE '' END, trouble = '', retry = '',
				updated_at = now() WHERE item_id = $1 AND language = $2`, itemID, w.lang, kind)
		case w.state == SubtitleHas:
			_, err = tx.Exec(ctx, `UPDATE subtitle_wants SET state = 'wanted', has_kind = '', subtitle_id = '', file_id = NULL,
				release = '', match = '', preview = '', attached_at = NULL, attached_how = '', next_at = now(),
				detail = 'the library no longer has one', trouble = 'the library lost it', retry = '', updated_at = now()
				WHERE item_id = $1 AND language = $2`, itemID, w.lang)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE subtitle_wants SET other_script = $3 WHERE item_id = $1 AND language = $2
			AND other_script <> $3`, itemID, w.lang, c.Other[w.lang]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SubtitleTitleByID reads one title; ErrNotFound when acquire looks none for it.
func (s *Store) SubtitleTitleByID(ctx context.Context, itemID string) (SubtitleTitle, error) {
	var t SubtitleTitle
	err := s.pool.QueryRow(ctx, `SELECT `+subtitleTitleCols+` FROM subtitle_titles t WHERE t.item_id = $1`, itemID).Scan(t.dest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubtitleTitle{}, ErrNotFound
	}
	return t, err
}

// SubtitleWantOf reads one want; ErrNotFound when there is none.
func (s *Store) SubtitleWantOf(ctx context.Context, k SubtitleKey) (SubtitleWant, error) {
	var w SubtitleWant
	err := s.pool.QueryRow(ctx, `SELECT `+subtitleWantCols+` FROM subtitle_wants w WHERE w.item_id = $1 AND w.language = $2`,
		k.ItemID, k.Language).Scan(w.dest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubtitleWant{}, ErrNotFound
	}
	return w, err
}

// SubtitleWantsOf lists a title's wants.
func (s *Store) SubtitleWantsOf(ctx context.Context, itemID string) ([]SubtitleWant, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+subtitleWantCols+` FROM subtitle_wants w WHERE w.item_id = $1 ORDER BY w.language`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleWant
	for rows.Next() {
		var w SubtitleWant
		if err := rows.Scan(w.dest()...); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DueSubtitle is a title with the languages it is due to be searched in.
type DueSubtitle struct {
	SubtitleTitle
	Languages []string
}

// subtitleOrder is the order work is taken in: what a person asked for in
// the console first, then new arrivals, what was asked for, and the rest of
// the library, the newest first.
const subtitleOrder = `t.priority, COALESCE(t.packaged_at, t.aired_at::timestamptz) DESC NULLS LAST, t.item_id`

// dueWant is a want of one of the languages $2 due to be searched: wanted
// (not searched yet, or due again), or not found or failed with its wait run
// out.
const dueWant = `(w.language = ANY($2) AND ((w.state = 'wanted' AND (w.next_at IS NULL OR w.next_at <= now()))
	OR (w.state IN ('notfound', 'failed') AND w.next_at <= now())))`

// DueSubtitleSearches lists up to limit titles due to be looked at, with the
// languages of langs due to be searched (dueWant): of titles that take a
// subtitle, those with such a language, and every title the catalog has not
// been asked about yet — one packaged again too, whose languages may all be
// had, as its new file may have lost one.
func (s *Store) DueSubtitleSearches(ctx context.Context, limit int, langs []string) ([]DueSubtitle, error) {
	if langs == nil {
		langs = []string{}
	}
	rows, err := s.pool.Query(ctx, `SELECT `+subtitleTitleCols+`,
			COALESCE(array_agg(w.language ORDER BY w.language) FILTER (WHERE `+dueWant+`), '{}')
		FROM subtitle_titles t LEFT JOIN subtitle_wants w ON w.item_id = t.item_id
		WHERE t.read_at IS NULL OR t.attachable
		GROUP BY t.item_id
		HAVING t.read_at IS NULL OR bool_or(`+dueWant+`)
		ORDER BY COALESCE(bool_or(w.asked_at IS NOT NULL), false) DESC, `+subtitleOrder+`
		LIMIT $1`, limit, langs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueSubtitle
	for rows.Next() {
		var d DueSubtitle
		if err := rows.Scan(append(d.dest(), &d.Languages)...); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SubtitleMatch is a found want with its title, in the order the day's
// downloads are spent.
type SubtitleMatch struct {
	SubtitleTitle
	Want SubtitleWant
}

// FoundSubtitles lists up to limit wants whose match waits for a download,
// in the order downloads are spent: what a person asked for first, then by
// the title's priority and age; with askedOnly, only what a person asked for.
func (s *Store) FoundSubtitles(ctx context.Context, limit int, askedOnly bool) ([]SubtitleMatch, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+subtitleTitleCols+`, `+subtitleWantCols+`
		FROM subtitle_wants w JOIN subtitle_titles t ON t.item_id = w.item_id
		WHERE w.state = 'found' AND t.attachable AND (NOT $2 OR w.asked_at IS NOT NULL)
		ORDER BY (w.asked_at IS NOT NULL) DESC, w.asked_at, `+subtitleOrder+`, w.language
		LIMIT $1`, limit, askedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleMatch
	for rows.Next() {
		var m SubtitleMatch
		if err := rows.Scan(append(m.dest(), m.Want.dest()...)...); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SubtitleSearchOutcome is what a search came to for a want: found (its
// best match, and how many usable versions it found), notfound, or wanted
// again (the search failed), what keeps the language from the title in plain
// words (Trouble) and in the source's own (Detail), and when it is looked at
// again.
type SubtitleSearchOutcome struct {
	State    string
	Found    any
	Versions int
	Trouble  string
	Detail   string
	Next     *time.Time
}

// RecordSubtitleSearch records a search's outcome for a want acquire does not
// have. A search that found nothing ends a person's asking. The failures in a
// row stay: a search that finds a match again is no download that worked.
func (s *Store) RecordSubtitleSearch(ctx context.Context, k SubtitleKey, o SubtitleSearchOutcome) error {
	var body []byte
	if o.Found != nil {
		b, err := json.Marshal(o.Found)
		if err != nil {
			return err
		}
		body = b
	}
	_, err := s.pool.Exec(ctx, `UPDATE subtitle_wants SET state = $3, found = $4, detail = $5, next_at = $6,
		trouble = $7, retry = '', versions = $8, searched_at = now(), searches = searches + 1,
		asked_at = CASE WHEN $3 = 'found' THEN asked_at END, updated_at = now()
		WHERE item_id = $1 AND language = $2 AND state <> 'has'`, k.ItemID, k.Language, o.State, body, clip(o.Detail, 500),
		o.Next, clip(o.Trouble, 120), o.Versions)
	return err
}

// SubtitleAttached is a subtitle acquire attached for a want.
type SubtitleAttached struct {
	SubtitleID string
	FileID     int64
	Release    string
	Match      string
	Preview    string
	How        string // auto | manual
	Kind       string // attached | traditional | ai
}

// RecordSubtitleAttached records the subtitle acquire attached for a want:
// it is had.
func (s *Store) RecordSubtitleAttached(ctx context.Context, k SubtitleKey, a SubtitleAttached) error {
	kind := a.Kind
	if kind == "" {
		kind = "attached"
	}
	_, err := s.pool.Exec(ctx, `UPDATE subtitle_wants SET state = 'has', has_kind = $3, subtitle_id = $4, file_id = $5,
		release = $6, match = $7, preview = $8, attached_at = now(), attached_how = $9, found = NULL, detail = '',
		trouble = '', retry = '', next_at = NULL, asked_at = NULL, failures = 0, updated_at = now()
		WHERE item_id = $1 AND language = $2`, k.ItemID, k.Language, kind, a.SubtitleID, a.FileID, clip(a.Release, 300),
		a.Match, clip(a.Preview, 4000), a.How)
	return err
}

// SubtitleFailure is a want's failed download or attach: what went wrong in
// plain words (Trouble) and in the source's or the catalog's own (Detail),
// what the next try does (Retry), and when it comes.
type SubtitleFailure struct {
	Trouble string
	Detail  string
	Retry   string
	Next    *time.Time
}

// RecordSubtitleFailed records that a want's download or attach failed: it
// needs attention, and is tried at next — or, after five failures in a row,
// not on its own any more (next nil). A failure whose next try is another
// version has one usable version less.
func (s *Store) RecordSubtitleFailed(ctx context.Context, k SubtitleKey, f SubtitleFailure) error {
	_, err := s.pool.Exec(ctx, `UPDATE subtitle_wants SET state = 'failed', detail = $3, trouble = $4, retry = $5,
		failures = failures + 1, next_at = CASE WHEN failures + 1 >= 5 THEN NULL ELSE $6::timestamptz END,
		versions = CASE WHEN $5 = 'another' THEN GREATEST(versions - 1, 0) WHEN $5 = 'none' THEN 0 ELSE versions END,
		updated_at = now() WHERE item_id = $1 AND language = $2 AND state <> 'has'`, k.ItemID, k.Language,
		clip(f.Detail, 500), clip(f.Trouble, 120), f.Retry, f.Next)
	return err
}

// BlockSubtitleFile records that the source's file fileID is never taken
// again for a want: a person removed it or put another in its place, or the
// catalog refused what it holds.
func (s *Store) BlockSubtitleFile(ctx context.Context, k SubtitleKey, fileID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE subtitle_wants SET blocked = blocked || to_jsonb($3::bigint), updated_at = now()
		WHERE item_id = $1 AND language = $2 AND NOT blocked @> to_jsonb($3::bigint)`, k.ItemID, k.Language, fileID)
	return err
}

// SubtitleTitleNotAttachable records that the catalog said the title takes
// no subtitle now, and why: its matches wait, and it is looked at again once
// it is packaged again.
func (s *Store) SubtitleTitleNotAttachable(ctx context.Context, itemID, why string) error {
	_, err := s.pool.Exec(ctx, `UPDATE subtitle_titles SET attachable = false, not_attachable = $2, read_at = now(),
		updated_at = now() WHERE item_id = $1`, itemID, clip(why, 500))
	return err
}

// DropSubtitleWant takes a want away: of a language no longer the
// instance's, whose subtitle acquire attached was removed.
func (s *Store) DropSubtitleWant(ctx context.Context, k SubtitleKey) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM subtitle_wants WHERE item_id = $1 AND language = $2`, k.ItemID, k.Language)
	return err
}

// ForgetAttachedSubtitle records that the subtitle acquire attached for a
// want was removed: the want is wanted again from next, and the file it was
// made of is never taken again for it.
func (s *Store) ForgetAttachedSubtitle(ctx context.Context, k SubtitleKey, next time.Time, detail string) error {
	_, err := s.pool.Exec(ctx, `UPDATE subtitle_wants SET state = 'wanted', has_kind = '', subtitle_id = '',
		blocked = CASE WHEN file_id IS NULL OR blocked @> to_jsonb(file_id) THEN blocked ELSE blocked || to_jsonb(file_id) END,
		file_id = NULL, release = '', match = '', preview = '', attached_at = NULL, attached_how = '', found = NULL,
		next_at = $3, detail = $4, trouble = 'removed', retry = '', updated_at = now() WHERE item_id = $1 AND language = $2`,
		k.ItemID, k.Language, next, clip(detail, 500))
	return err
}

// AskSubtitles puts the wants named first in line, to be searched now (one
// not found, or failed, is wanted again; a found one is downloaded first);
// one had is left alone. It answers the wants it asked for.
func (s *Store) AskSubtitles(ctx context.Context, keys []SubtitleKey) ([]SubtitleKey, error) {
	items, langs := make([]string, len(keys)), make([]string, len(keys))
	for i, k := range keys {
		items[i], langs[i] = k.ItemID, k.Language
	}
	rows, err := s.pool.Query(ctx, `UPDATE subtitle_wants w SET asked_at = now(),
		next_at = CASE WHEN w.state = 'found' THEN w.next_at ELSE now() END,
		state = CASE WHEN w.state IN ('notfound', 'failed') THEN 'wanted' ELSE w.state END,
		failures = CASE WHEN w.state = 'failed' THEN 0 ELSE w.failures END, updated_at = now()
		FROM unnest($1::text[], $2::text[]) AS k(item_id, language)
		WHERE w.item_id = k.item_id AND w.language = k.language AND w.state <> 'has'
		RETURNING w.item_id, w.language`, items, langs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleKey
	for rows.Next() {
		var k SubtitleKey
		if err := rows.Scan(&k.ItemID, &k.Language); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RenewSubtitleTitle makes a title new again: packaged once more, its file
// may be another. The catalog is asked again (its original's fingerprint
// too), whatever its languages' states; every language it has not is
// wanted now, a match found for the file before dropped, its failures
// forgotten.
func (s *Store) RenewSubtitleTitle(ctx context.Context, itemID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE subtitle_titles SET read_at = NULL, sum64 = '', size_bytes = NULL, labels = '{}'::jsonb,
		original_note = '', updated_at = now() WHERE item_id = $1`, itemID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE subtitle_wants SET state = 'wanted', found = NULL, next_at = now(), failures = 0,
		detail = CASE WHEN searched_at IS NULL THEN detail ELSE 'packaged again' END,
		trouble = CASE WHEN searched_at IS NULL THEN trouble ELSE 'packaged again' END, retry = '', updated_at = now()
		WHERE item_id = $1 AND state <> 'has'`, itemID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SubtitleDownload is one download asked of a subtitle source.
type SubtitleDownload struct {
	At        time.Time `json:"at"`
	ItemID    string    `json:"itemId"`
	Language  string    `json:"language"`
	FileID    int64     `json:"fileId"`
	How       string    `json:"how"` // auto | manual
	OK        bool      `json:"ok"`
	Remaining *int      `json:"remaining"`
	Detail    string    `json:"detail"`
}

// RecordSubtitleDownload records a download asked of the source.
func (s *Store) RecordSubtitleDownload(ctx context.Context, d SubtitleDownload) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO subtitle_downloads (item_id, language, file_id, how, ok, remaining, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, d.ItemID, d.Language, d.FileID, d.How, d.OK, d.Remaining, clip(d.Detail, 300))
	return err
}

// SubtitleDownloadsSince counts the downloads asked since since, the source
// took or not (each counts against its quota as soon as it is asked), of how
// ("" for every kind).
func (s *Store) SubtitleDownloadsSince(ctx context.Context, since time.Time, how string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM subtitle_downloads WHERE at >= $1 AND ok AND ($2 = '' OR how = $2)`,
		since, how).Scan(&n)
	return n, err
}

// SubtitleRow is a want with its title, as the console lists it.
type SubtitleRow struct {
	SubtitleTitle
	Want SubtitleWant `json:"want"`
}

// SubtitleFilter narrows the console's list: the states, a language, words
// of the title (or S04E07).
type SubtitleFilter struct {
	States   []string
	Language string
	Query    string
	Limit    int
}

// SubtitleRows lists wants with their titles for the console, in the order
// work is taken in.
func (s *Store) SubtitleRows(ctx context.Context, f SubtitleFilter) ([]SubtitleRow, error) {
	if f.Limit <= 0 {
		f.Limit = 1000
	}
	if f.States == nil {
		f.States = []string{}
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	rows, err := s.pool.Query(ctx, `SELECT `+subtitleTitleCols+`, `+subtitleWantCols+`
		FROM subtitle_wants w JOIN subtitle_titles t ON t.item_id = w.item_id
		WHERE (cardinality($1::text[]) = 0 OR w.state = ANY($1))
		  AND ($2 = '' OR w.language = $2)
		  AND ($3 = '' OR lower(t.title) LIKE '%' || $3 || '%'
		       OR 's' || lpad(t.season::text, 2, '0') || 'e' || lpad(t.episode::text, 2, '0') = replace($3, ' ', ''))
		ORDER BY (w.asked_at IS NOT NULL) DESC, `+subtitleOrder+`, w.language
		LIMIT $4`, f.States, f.Language, q, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubtitleRow{}
	for rows.Next() {
		var r SubtitleRow
		if err := rows.Scan(append(r.dest(), r.Want.dest()...)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SubtitleStateCounts counts the wants by state, of one language ("" for
// every one); "<state>:asked" counts those of the state a person asked for.
func (s *Store) SubtitleStateCounts(ctx context.Context, language string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT state, count(*), count(*) FILTER (WHERE asked_at IS NOT NULL)
		FROM subtitle_wants WHERE $1 = '' OR language = $1 GROUP BY state`, language)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var state string
		var n, asked int
		if err := rows.Scan(&state, &n, &asked); err != nil {
			return nil, err
		}
		out[state], out[state+":asked"] = n, asked
	}
	return out, rows.Err()
}

// SubtitleCoverage is one language's wants: how many titles have it, of how
// many.
type SubtitleCoverage struct {
	Language string `json:"language"`
	Has      int    `json:"has"`
	Total    int    `json:"total"`
}

// SubtitleCoverageByLanguage counts each language's wants and those had.
func (s *Store) SubtitleCoverageByLanguage(ctx context.Context) ([]SubtitleCoverage, error) {
	rows, err := s.pool.Query(ctx, `SELECT language, count(*) FILTER (WHERE state = 'has'), count(*)
		FROM subtitle_wants GROUP BY language ORDER BY language`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleCoverage
	for rows.Next() {
		var c SubtitleCoverage
		if err := rows.Scan(&c.Language, &c.Has, &c.Total); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FoundSubtitleRank is the place of each found want in the download order
// (0 first): the queue the day's downloads are spent on.
func (s *Store) FoundSubtitleRank(ctx context.Context) (map[SubtitleKey]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT w.item_id, w.language FROM subtitle_wants w JOIN subtitle_titles t ON t.item_id = w.item_id
		WHERE w.state = 'found' AND t.attachable
		ORDER BY (w.asked_at IS NOT NULL) DESC, w.asked_at, `+subtitleOrder+`, w.language`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[SubtitleKey]int{}
	for rows.Next() {
		var k SubtitleKey
		if err := rows.Scan(&k.ItemID, &k.Language); err != nil {
			return nil, err
		}
		out[k] = len(out)
	}
	return out, rows.Err()
}

// clip cuts s to at most n bytes, at a rune's start, an ellipsis last.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
