package app

import (
	"context"
	"fmt"

	"github.com/laedeli/acquire/internal/storage"
	"github.com/laedeli/acquire/internal/store"
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
//
// The space that matters is where THIS client writes, seen from acquire: the
// client's local folder, or ACQUIRE_DOWNLOADS_ROOT for a client that declares
// none. The floor and the concurrency cap are the admin's search and grab
// settings.
func (s *Service) AdmitGrab(ctx context.Context, c store.DownloadClient, sizeBytes int64) error {
	pol := s.grabPolicy(ctx)
	g := storage.Guard{
		Path:          s.downloadsPath(c),
		FloorBytes:    pol.StorageFloorGB << 30,
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
	if max := pol.MaxConcurrentGrabs; active >= max {
		return fmt.Errorf("refusing grab: %d download(s) already in flight (cap %d)", active, max)
	}
	return nil
}

// downloadsPath is where a client's downloads are visible to acquire.
func (s *Service) downloadsPath(c store.DownloadClient) string {
	if c.LocalPath != "" {
		return c.LocalPath
	}
	return s.cfg.DownloadsRoot
}

// FreeBytes powers the metric, so disk pressure is visible before it bites.
func (s *Service) FreeBytes() int64 {
	n, err := storage.Guard{Path: s.cfg.DownloadsRoot}.Free()
	if err != nil {
		return -1
	}
	return n
}

// StorageFloorBytes is the admin's configured floor.
func (s *Service) StorageFloorBytes(ctx context.Context) int64 {
	return s.grabPolicy(ctx).StorageFloorGB << 30
}
