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
	if !remux.Rejected {
		t.Errorf("a 73 GB release should breach the size cap: %+v", remux)
	}
	if encode.Rejected {
		t.Fatalf("the encode should be acceptable: %+v", encode)
	}
	if encode.Score <= 0 {
		t.Errorf("encode scored %d", encode.Score)
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
	p := DefaultProfile() // 1080p first, then 2160p
	hd, _ := Score(Candidate{Title: "M.2020.1080p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 6000}, p)
	uhd, _ := Score(Candidate{Title: "M.2020.2160p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 6000}, p)
	if hd.Score <= uhd.Score {
		t.Fatalf("1080p (%d) is listed first so it should win over 2160p (%d)", hd.Score, uhd.Score)
	}
}
