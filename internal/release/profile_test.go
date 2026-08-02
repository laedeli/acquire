package release

import "testing"

func TestParseTypicalReleaseNames(t *testing.T) {
	in := Parse("The.Matrix.1999.2160p.UHD.BluRay.REMUX.HEVC.DTS-HD.MA.TrueHD.7.1-FGT")
	if in.Resolution != "2160p" || in.Source != "remux" || in.Codec != "x265" {
		t.Fatalf("parsed %+v", in)
	}
	if in.Year != 1999 || in.Group != "FGT" {
		t.Errorf("year/group = %d/%q", in.Year, in.Group)
	}

	ep := Parse("Some.Show.S02E07.1080p.WEB-DL.x264-GROUP")
	if ep.Season != 2 || ep.Episode != 7 || ep.Source != "webdl" || ep.Codec != "x264" {
		t.Fatalf("episode parse %+v", ep)
	}

	hdr := Parse("Movie.2021.2160p.WEB-DL.DDP5.1.HDR.HEVC-Group")
	if !hdr.HDR {
		t.Error("HDR not detected")
	}
}

func TestParseCountsLanguages(t *testing.T) {
	in := Parse("The.Matrix.1999.Eng.Fre.Ger.Ita.Por.Spa.2160p.BluRay.Remux.HEVC-SGF")
	if len(in.Languages) < 5 {
		t.Fatalf("languages = %v", in.Languages)
	}
}

// The whole point of the profile: a sane 1080p x265 encode must beat the
// enormous multi-language remux that the old size-descending rule kept picking.
func TestEncodeBeatsBloatedRemux(t *testing.T) {
	p := DefaultProfile()
	remux, _ := Score(Candidate{
		Title:    "The.Matrix.1999.Eng.Fre.Ger.Ita.Por.Spa.Cze.Hun.Pol.2160p.BluRay.Remux.HEVC.Atmos-SGF",
		Protocol: "usenet", SizeMb: 73 * 1024,
	}, p)
	encode, _ := Score(Candidate{
		Title:    "The.Matrix.1999.1080p.BluRay.x265-GROUP",
		Protocol: "usenet", SizeMb: 8 * 1024,
	}, p)
	// The remux is NOT rejected on size any more — a 2160p remux is a legitimate
	// thing to hold, and per-resolution maxima exist so we can allow it. It has
	// to LOSE on merit instead: nine audio languages cost more than the remux
	// source is worth, which is exactly the wrong-default-audio pathology.
	if remux.Rejected {
		t.Errorf("a 73 GB 2160p remux is under the 2160p cap and should be allowed: %+v", remux)
	}
	if encode.Rejected {
		t.Fatalf("the encode should be acceptable: %+v", encode)
	}
	if encode.Score <= remux.Score {
		t.Errorf("the encode (%d) must beat the 9-language remux (%d)", encode.Score, remux.Score)
	}
}

func TestProtocolPreferenceDominates(t *testing.T) {
	p := DefaultProfile()
	nzb, _ := Score(Candidate{Title: "Movie.2020.720p.WEB-DL.x264-G", Protocol: "usenet", SizeMb: 2000}, p)
	tor, _ := Score(Candidate{
		Title: "Movie.2020.1080p.BluRay.x265-G", Protocol: "torrent", SizeMb: 8000, Seeders: 500,
	}, p)
	if nzb.Score <= tor.Score {
		t.Fatalf("NZB (%d) should outrank a torrent (%d) when usenet is preferred", nzb.Score, tor.Score)
	}
}

func TestRejections(t *testing.T) {
	p := DefaultProfile()
	cam, _ := Score(Candidate{Title: "Movie 2024 CAM x264", Protocol: "usenet", SizeMb: 1500}, p)
	if !cam.Rejected {
		t.Error("a cam should be rejected")
	}
	dead, _ := Score(Candidate{
		Title: "Movie.2020.1080p.BluRay.x265-G", Protocol: "torrent", SizeMb: 8000, Seeders: 0,
	}, p)
	if !dead.Rejected {
		t.Error("a 0-seeder torrent should be rejected when minSeeders is 1")
	}
	tiny, _ := Score(Candidate{Title: "Movie.2020.1080p.x265-G", Protocol: "usenet", SizeMb: 50}, p)
	if !tiny.Rejected {
		t.Error("a 50 MB 'movie' should be rejected")
	}
}

func TestResolutionOrderFollowsProfile(t *testing.T) {
	p := DefaultProfile() // 2160p first, then 1080p
	hd, _ := Score(Candidate{Title: "M.2020.1080p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 6000}, p)
	uhd, _ := Score(Candidate{Title: "M.2020.2160p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 6000}, p)
	if uhd.Score <= hd.Score {
		t.Fatalf("2160p (%d) is listed first so it should win over 1080p (%d)", uhd.Score, hd.Score)
	}
	// And the order must follow the profile, not a hardcoded rank.
	flipped := DefaultProfile()
	flipped.Resolutions = []string{"1080p", "2160p", "720p"}
	hd2, _ := Score(Candidate{Title: "M.2020.1080p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 6000}, flipped)
	uhd2, _ := Score(Candidate{Title: "M.2020.2160p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 6000}, flipped)
	if hd2.Score <= uhd2.Score {
		t.Fatalf("with 1080p listed first it should win: 1080p=%d 2160p=%d", hd2.Score, uhd2.Score)
	}
}

// The reject list used to be matched against the RAW title with space padding,
// so no dot-separated scene name could ever match it. A cam release scored
// higher than a legitimate WEB-DL on the live pod.
func TestCamFamilyIsRejected(t *testing.T) {
	p := DefaultProfile()
	for _, title := range []string{
		"Movie.2024.HDCAM.x264-GRP",
		"Movie.2024.CAMRip.XviD-GRP",
		"Movie.2024.HDTS.x264-GRP",
		"Movie.2024.TELESYNC.x264-GRP",
		"Movie.2024.1080p.SCREENER.x264-GRP",
	} {
		v, in := Score(Candidate{Title: title, Protocol: "usenet", SizeMb: 2000}, p)
		if !v.Rejected {
			t.Errorf("%s must be rejected (parsed source %q, score %d)", title, in.Source, v.Score)
		}
	}
	// ...and it must outrank nothing: a real WEB-DL always wins.
	cam, _ := Score(Candidate{Title: "Movie.2024.HDCAM.x264-GRP", Protocol: "usenet", SizeMb: 1500}, p)
	web, _ := Score(Candidate{Title: "Movie.2024.1080p.WEB-DL.x265-GRP", Protocol: "usenet", SizeMb: 4000}, p)
	if !cam.Rejected || web.Rejected || web.Score <= cam.Score {
		t.Errorf("cam=%+v web=%+v", cam, web)
	}
}

// Tightening cam detection must not swallow innocent names. These are the
// false positives a loose substring match produces.
func TestCamDetectionDoesNotEatInnocentNames(t *testing.T) {
	p := DefaultProfile()
	for _, title := range []string{
		"Film.2024.1080p.BluRay.x265-CAMELOT", // a release GROUP containing "cam"
		"Cam.2018.1080p.WEBRip.x264-GROUP",    // a real film literally called Cam
	} {
		v, in := Score(Candidate{Title: title, Protocol: "usenet", SizeMb: 4000}, p)
		if v.Rejected {
			t.Errorf("%s must NOT be rejected (parsed source %q): %v", title, in.Source, v.Reasons)
		}
	}
}

// Source was parsed and displayed but worth zero points, so the size penalty
// ranked a small HDTV rip above a Blu-ray remux.
func TestSourceRankingBeatsTheSizePenalty(t *testing.T) {
	p := DefaultProfile()
	score := func(title string, mb int64) int {
		v, _ := Score(Candidate{Title: title, Protocol: "usenet", SizeMb: mb}, p)
		if v.Rejected {
			t.Fatalf("%s unexpectedly rejected: %v", title, v.Reasons)
		}
		return v.Score
	}
	remux := score("M.2020.1080p.BluRay.REMUX.x265-G", 30000)
	bluray := score("M.2020.1080p.BluRay.x265-G", 12000)
	webdl := score("M.2020.1080p.WEB-DL.x265-G", 6000)
	hdtv := score("M.2020.1080p.HDTV.x265-G", 2000)
	if !(remux > bluray && bluray > webdl && webdl > hdtv) {
		t.Errorf("source order broken: remux=%d bluray=%d webdl=%d hdtv=%d", remux, bluray, webdl, hdtv)
	}
}

// One global cap cannot both allow a 60 GB 2160p remux and reject a bloated
// 720p rip. Maxima are per resolution; there are deliberately no minima beyond
// the global junk floor.
func TestPerResolutionSizeCaps(t *testing.T) {
	p := DefaultProfile()
	big2160, _ := Score(Candidate{Title: "M.2020.2160p.BluRay.REMUX.x265-G", Protocol: "usenet", SizeMb: 60000}, p)
	if big2160.Rejected {
		t.Errorf("a 60 GB 2160p remux is legitimate and must be allowed: %v", big2160.Reasons)
	}
	big720, _ := Score(Candidate{Title: "M.2020.720p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 60000}, p)
	if !big720.Rejected {
		t.Errorf("a 60 GB 720p release is junk and must be rejected: %+v", big720)
	}
	// The smallest legitimate 2160p grab measured in prod was 1,208 MB — no
	// minimum may reject it beyond the global 500 MB junk floor.
	small2160, _ := Score(Candidate{Title: "M.2020.2160p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 1208}, p)
	if small2160.Rejected {
		t.Errorf("a 1,208 MB 2160p release is real and must be allowed: %+v", small2160)
	}
}

// The penalty is per extra language and must be able to outweigh a source
// bonus, or a 10-language remux wins on the remux term alone.
func TestLanguagePenaltyScalesAgainstSourceBonus(t *testing.T) {
	p := DefaultProfile()
	clean, _ := Score(Candidate{
		Title: "M.2020.1080p.BluRay.REMUX.x265-G", Protocol: "usenet", SizeMb: 25000}, p)
	multi, _ := Score(Candidate{
		Title:    "M.2020.Eng.Ger.Fre.Ita.Spa.Rus.Jpn.Pol.Cze.Hun.1080p.BluRay.REMUX.x265-G",
		Protocol: "usenet", SizeMb: 25000}, p)
	if multi.Score >= clean.Score {
		t.Errorf("a 10-language remux (%d) must lose to the same clean remux (%d)", multi.Score, clean.Score)
	}
	if clean.Score-multi.Score < p.SourceScores["remux"] {
		t.Errorf("the penalty (%d) is too small to outweigh the remux bonus (%d)",
			clean.Score-multi.Score, p.SourceScores["remux"])
	}
}
