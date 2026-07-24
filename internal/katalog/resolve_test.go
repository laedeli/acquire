package katalog

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveVideoDir: the largest video file in a download folder wins (skips a
// small sample), and non-video files are ignored.
func TestResolveVideoDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, make([]byte, size), 0o644)
		return p
	}
	write("readme.txt", 10)
	write("sample.mkv", 100)
	big := write("feature.mkv", 5000)

	if got := ResolveVideo([]string{dir}); got != big {
		t.Errorf("ResolveVideo dir = %q, want %q", got, big)
	}
}

// TestResolveVideoFile: a direct video file path is returned as-is.
func TestResolveVideoFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "movie.mp4")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	if got := ResolveVideo([]string{p}); got != p {
		t.Errorf("ResolveVideo file = %q, want %q", got, p)
	}
	// A non-existent, non-video path yields "".
	if got := ResolveVideo([]string{filepath.Join(dir, "notes.txt")}); got != "" {
		t.Errorf("expected empty for non-video, got %q", got)
	}
}
