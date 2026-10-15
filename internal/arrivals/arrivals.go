// Package arrivals copies finished downloads into the library's arrivals.
//
// The download clients the chart runs work on a volume of their own; the
// catalog takes files from its arrivals on the media storage only. So a
// finished download is copied there before it is handed over — gently, since
// that storage serves playback: one file at a time, in one sequential pass,
// into a temporary name beside the target (a dot name, which the arrivals scan
// passes over), synced to disk, checked, and only then renamed into place in
// one step. A copy that fails leaves nothing behind but the source, which is
// never touched.
package arrivals

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// chunk is what one read and one write move.
	chunk = 4 << 20
	// sample is how much of each end of a file the check hashes.
	sample = 1 << 20
	// FileMode and DirMode are what the platform's writers of the share create
	// (umask 0002: the group writes too).
	FileMode os.FileMode = 0o664
	DirMode  os.FileMode = 0o775
)

// Copy copies the file src to dst (whose folder it makes) and returns once the
// copy is in place: written through a temporary name in one sequential pass,
// synced, verified against the source (Verify), renamed onto dst and the folder
// synced. An existing dst is replaced. On any failure the temporary file is
// removed and dst is left as it was.
func Copy(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if info, err := in.Stat(); err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is no regular file", src)
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(dst)+"."+token()+".copying")
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, FileMode)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			out.Close()
			os.Remove(tmp)
		}
	}()
	buf := make([]byte, chunk)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, FileMode); err != nil {
		return err
	}
	if err := Verify(src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	done = true
	return syncDir(dir)
}

// Verify checks that dst holds what src holds, cheaply on any size: the same
// length, and the same SHA-256 over the first and the last MiB.
func Verify(src, dst string) error {
	a, err := fingerprint(src)
	if err != nil {
		return err
	}
	b, err := fingerprint(dst)
	if err != nil {
		return err
	}
	if a.size != b.size {
		return fmt.Errorf("the copy of %s holds %d bytes, the download %d", filepath.Base(src), b.size, a.size)
	}
	if !bytes.Equal(a.sum, b.sum) {
		return fmt.Errorf("the copy of %s differs from the download", filepath.Base(src))
	}
	return nil
}

type print struct {
	size int64
	sum  []byte
}

func fingerprint(path string) (print, error) {
	f, err := os.Open(path)
	if err != nil {
		return print{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return print{}, err
	}
	h := sha256.New()
	head := min(info.Size(), sample)
	if _, err := io.CopyN(h, f, head); err != nil {
		return print{}, err
	}
	if tail := min(info.Size()-head, sample); tail > 0 {
		if _, err := f.Seek(info.Size()-tail, io.SeekStart); err != nil {
			return print{}, err
		}
		if _, err := io.CopyN(h, f, tail); err != nil {
			return print{}, err
		}
	}
	return print{size: info.Size(), sum: h.Sum(nil)}, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func token() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// subtitleExts and langSuffix are the catalog's: a subtitle file named as its
// video, a language between maybe, is the video's sidecar.
var (
	subtitleExts = map[string]bool{".srt": true, ".vtt": true, ".ass": true, ".ssa": true}
	langSuffix   = regexp.MustCompile(`\.([a-zA-Z]{2,3}(?:[-_][a-zA-Z]{2,4})?)$`)
)

// Sidecars lists the subtitle files beside video that the catalog pairs with
// it: in its folder, named as the video (Film.srt, Film.en.srt beside
// Film.mkv), letter case aside.
func Sidecars(video string) []string {
	dir := filepath.Dir(video)
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || !subtitleExts[ext] {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if m := langSuffix.FindStringIndex(base); m != nil && strings.EqualFold(base[:m[0]], stem) {
			base = base[:m[0]]
		}
		if strings.EqualFold(base, stem) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}
