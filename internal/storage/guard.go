// Package storage refuses to start a download that the disk cannot take.
//
// This is not theoretical. The media export is 53 TB at 92% with 4.5 TB free,
// and beta shares the SAME physical filesystem as production — the beta PVC's
// declared size is not a quota, because NFS enforces nothing. An unbounded
// sweep that grabbed would consume production's remaining headroom, and acquire
// has no deletion code at all.
package storage

import (
	"fmt"
	"syscall"
)

// Guard checks free space on the path downloads land in.
type Guard struct {
	Path string
	// FloorBytes is the free space below which grabs are refused outright.
	FloorBytes int64
	// HeadroomBytes must remain free AFTER the download completes, so a grab
	// cannot be the thing that fills the disk.
	HeadroomBytes int64
	// FreeAt reads the free space at a path; nil reads the filesystem (statfs).
	FreeAt func(path string) (int64, error)
}

// Free reports bytes available to an unprivileged writer.
//
// Bavail, not Bfree: Bfree counts blocks reserved for root, which we cannot
// use. Reporting Bfree would let a grab through that then fails part-written.
func (g Guard) Free() (int64, error) {
	if g.Path == "" {
		return 0, fmt.Errorf("storage guard: no path configured")
	}
	if g.FreeAt != nil {
		return g.FreeAt(g.Path)
	}
	return FreeAt(g.Path)
}

// FreeAt reports the bytes available to an unprivileged writer at path.
func FreeAt(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// Admit decides whether a download of sizeBytes may start.
//
// It fails CLOSED: if free space cannot be determined, the grab is refused. An
// unreadable mount is exactly when you least want to start writing to it, and
// "I could not check" must never read as "there is room" — the same mistake as
// treating a failed library lookup as "you do not own this".
func (g Guard) Admit(sizeBytes int64) error {
	free, err := g.Free()
	if err != nil {
		return fmt.Errorf("refusing grab: %w", err)
	}
	if g.FloorBytes > 0 && free < g.FloorBytes {
		return fmt.Errorf("refusing grab: %s free is below the %s floor",
			Human(free), Human(g.FloorBytes))
	}
	if need := sizeBytes + g.HeadroomBytes; sizeBytes > 0 && free < need {
		return fmt.Errorf("refusing grab: needs %s (%s + %s headroom) but only %s free",
			Human(need), Human(sizeBytes), Human(g.HeadroomBytes), Human(free))
	}
	return nil
}

// Human writes a byte count as an operator reads it.
func Human(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
}
