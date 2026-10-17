package arrivals

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestCopy(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "downloads", "complete", "Film.2020", "Film.2020.mkv")
	data := make([]byte, 3*chunk+12345) // several chunks, and both sampled ends
	_, _ = rand.Read(data)
	write(t, src, data)
	dst := filepath.Join(root, "arrivals", "acquire", "w_1", "Film.2020.mkv")

	if err := Copy(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the copy differs (%d bytes, %v)", len(got), err)
	}
	if info, _ := os.Stat(dst); info.Mode().Perm() != FileMode {
		t.Errorf("mode %v, want %v", info.Mode().Perm(), FileMode)
	}
	if names := entries(t, filepath.Dir(dst)); !reflect.DeepEqual(names, []string{"Film.2020.mkv"}) {
		t.Errorf("left beside the copy: %v", names)
	}
	if b, err := os.ReadFile(src); err != nil || !bytes.Equal(b, data) {
		t.Fatal("the source was changed")
	}
	if err := Verify(src, dst); err != nil {
		t.Errorf("verify: %v", err)
	}
}

// A copy that cannot finish leaves the target as it was, no temporary file,
// and the source untouched.
func TestCopyThatFailsLeavesNothing(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "Film.mkv")
	write(t, src, []byte("the download"))

	if err := Copy(context.Background(), filepath.Join(root, "missing.mkv"), filepath.Join(root, "out", "x.mkv")); err == nil {
		t.Error("a missing source was copied")
	}

	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := Copy(context.Background(), src, filepath.Join(locked, "Film.mkv")); err == nil {
		t.Error("copied into a folder that cannot be written")
	}
	if names := entries(t, locked); len(names) != 0 {
		t.Errorf("left behind: %v", names)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(root, "out")
	if err := Copy(ctx, src, filepath.Join(out, "Film.mkv")); err == nil {
		t.Error("a cancelled copy finished")
	}
	if names := entries(t, out); len(names) != 0 {
		t.Errorf("a cancelled copy left %v", names)
	}
	if b, _ := os.ReadFile(src); string(b) != "the download" {
		t.Error("the source was changed")
	}
}

func TestVerify(t *testing.T) {
	root := t.TempDir()
	data := make([]byte, 3*sample)
	_, _ = rand.Read(data)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, a, data)

	write(t, b, data[:len(data)-1])
	if err := Verify(a, b); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Errorf("a short copy: %v", err)
	}
	tail := append([]byte{}, data...)
	tail[len(tail)-1] ^= 0xff
	write(t, b, tail)
	if err := Verify(a, b); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Errorf("a copy whose end differs: %v", err)
	}
	write(t, b, data)
	if err := Verify(a, b); err != nil {
		t.Errorf("an exact copy: %v", err)
	}
	small := filepath.Join(root, "small")
	write(t, small, []byte("x"))
	if err := Verify(small, small); err != nil {
		t.Errorf("a file smaller than a sample: %v", err)
	}
}

func TestSidecars(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Film.2020.mkv", "Film.2020.srt", "film.2020.EN.srt", "Film.2020.de-CH.ass",
		"Film.2020.nfo", "Other.srt", "Film.2020.sample.srt", "Film.2020.forced.srt.txt"} {
		write(t, filepath.Join(dir, name), []byte("x"))
	}
	var got []string
	for _, p := range Sidecars(filepath.Join(dir, "Film.2020.mkv")) {
		got = append(got, filepath.Base(p))
	}
	sort.Strings(got)
	want := []string{"Film.2020.de-CH.ass", "Film.2020.srt", "film.2020.EN.srt"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sidecars %v, want %v", got, want)
	}
}

// TestCopySyncsOnTheWay: a copy is synced every syncEvery bytes on its way and
// still arrives whole, with a length that is no multiple of the interval.
func TestCopySyncsOnTheWay(t *testing.T) {
	old := syncEvery
	syncEvery = 1 << 10
	defer func() { syncEvery = old }()
	dir := t.TempDir()
	data := make([]byte, 10<<10+123)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "downloads", "Example.Show.S01E01.mkv")
	write(t, src, data)
	dst := filepath.Join(dir, "arrivals", "acquire", "tg_1", "client-1", "Example.Show.S01E01.mkv")
	if err := Copy(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("the copy differs: %d bytes, want %d", len(got), len(data))
	}
	if names := entries(t, filepath.Dir(dst)); !reflect.DeepEqual(names, []string{"Example.Show.S01E01.mkv"}) {
		t.Fatalf("the folder holds %v", names)
	}
}

// TestRemoveLeftovers: only a copy's temporary file that has not been written
// for longer than the age is removed; a fresh one (a copy that still runs), the
// finished files and names that only look alike stay.
func TestRemoveLeftovers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "acquire")
	now := time.Now()
	old := now.Add(-3 * time.Hour)
	files := map[string]time.Time{
		"tg_1/nzbget-1/.Example.Show.S01E08.mkv.ecf599225ae1.copying":     old,                        // abandoned: removed
		"tg_2/nzbget-2/.Example.Show.S01E09.mkv.0123456789ab.copying":     now.Add(-10 * time.Minute), // still copied
		"tg_1/nzbget-1/Example.Show.S01E08.mkv":                           old,                        // the finished copy
		"tg_1/nzbget-1/.notes.copying":                                    old,                        // no token
		"tg_1/nzbget-1/.Example.Show.S01E08.mkv.ecf599225ae1.copying.bak": old,                        // another name
	}
	for rel, mtime := range files {
		p := filepath.Join(root, rel)
		write(t, p, []byte(strings.Repeat("x", 100)))
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	removed, n, err := RemoveLeftovers(root, 2*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "tg_1/nzbget-1/.Example.Show.S01E08.mkv.ecf599225ae1.copying")}
	if !reflect.DeepEqual(removed, want) || n != 100 {
		t.Fatalf("removed %v (%d bytes), want %v (100 bytes)", removed, n, want)
	}
	for rel := range files {
		_, err := os.Stat(filepath.Join(root, rel))
		if gone := os.IsNotExist(err); gone != (filepath.Join(root, rel) == want[0]) {
			t.Errorf("%s: gone=%v", rel, gone)
		}
	}
	// No arrivals folder yet: nothing to remove, and no error.
	if removed, _, err := RemoveLeftovers(filepath.Join(t.TempDir(), "none"), time.Hour, now); err != nil || len(removed) != 0 {
		t.Fatalf("a missing root: %v %v", removed, err)
	}
}
