package app

import (
	"context"
	"fmt"

	"github.com/laedeli/acquire/internal/storage"
)

// AdmitGrab is the last gate before anything is downloaded.
//
// Two bounds, both absent from the system until now:
//
//  1. Free space. The media export is at 92% with 4.5 TB left, and beta shares
//     the SAME physical filesystem as production — the beta PVC size is not a
//     quota because NFS enforces nothing. acquire has no deletion code, so
//     every grab is permanent growth.
//  2. Concurrency. Nothing capped in-flight downloads, so a backlog sweep over
//     21,537 episodes could start hundreds at once and fill the disk long
//     before the first finished.
//
// Both fail CLOSED. A grab that cannot be shown to be safe does not happen.
func (s *Service) AdmitGrab(ctx context.Context, sizeBytes int64) error {
	g := storage.Guard{
		Path:          s.cfg.DownloadsRoot,
		FloorBytes:    s.cfg.StorageFloorBytes(),
		HeadroomBytes: 50 << 30, // never let a grab be the thing that fills it
	}
	if err := g.Admit(sizeBytes); err != nil {
		return err
	}
	active, err := s.st.ActiveDownloads(ctx)
	if err != nil {
		// Cannot count in-flight work: refuse rather than guess.
		return fmt.Errorf("refusing grab: cannot count active downloads: %w", err)
	}
	if max := s.cfg.MaxConcurrentGrabs(); active >= max {
		return fmt.Errorf("refusing grab: %d download(s) already in flight (cap %d)", active, max)
	}
	return nil
}

// FreeBytes powers the metric, so disk pressure is visible before it bites.
func (s *Service) FreeBytes() int64 {
	n, err := storage.Guard{Path: s.cfg.DownloadsRoot}.Free()
	if err != nil {
		return -1
	}
	return n
}
