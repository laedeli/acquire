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
//
// A client the deployment runs works on a downloads volume of its own, and
// its finished files are copied into the library (imports.go). That volume is
// a local disk without a quota, shared with other volumes, so it must hold
// the whole release before it is handed over: twice its size for an NZB (the
// archive and its unpacking), its size for a torrent, and the reserve
// (ACQUIRE_DOWNLOADS_RESERVE) on top. The media storage the copy lands on
// keeps the floor. A release turned away waits, and the request says why.
func (s *Service) AdmitGrab(ctx context.Context, c store.DownloadClient, sizeBytes int64, protocol string) error {
	pol := s.grabPolicy(ctx)
	g := storage.Guard{
		Path:          s.downloadsPath(c),
		FloorBytes:    pol.StorageFloorGB << 30,
		HeadroomBytes: 50 << 30, // never let a grab be the thing that fills it
		FreeAt:        s.freeSpace,
	}
	if s.onDownloadsVolume(c) {
		if err := s.admitDownloadsVolume(c, sizeBytes, protocol); err != nil {
			return err
		}
		g.Path = s.cfg.ArrivalsRoot
	}
	if err := g.Admit(sizeBytes); err != nil {
		return err
	}
	if full, why := s.slotsFull(ctx); full {
		return atCapacity{msg: "refusing grab: " + why}
	}
	return nil
}

// atCapacity is a grab turned away because as many downloads as the policy
// allows are in flight (or they cannot be counted). It passes once one ends.
type atCapacity struct{ msg string }

func (e atCapacity) Error() string { return e.msg }

// slotsFull reports whether no further download may start now, and why. It
// counts what the downloads table shows and the grabs handed over that the
// gateway has not reported yet (store.ActiveDownloads). A count that cannot be
// read is full: refuse rather than guess.
func (s *Service) slotsFull(ctx context.Context) (bool, string) {
	active, err := s.st.ActiveDownloads(ctx)
	if err != nil {
		return true, fmt.Sprintf("cannot count active downloads: %v", err)
	}
	if max := s.grabPolicy(ctx).MaxConcurrentGrabs; active >= max {
		return true, fmt.Sprintf("%d download(s) already in flight (cap %d)", active, max)
	}
	return false, ""
}

// onDownloadsVolume reports whether a client works on the deployment's
// downloads volume: the clients it runs, when imports are copies.
func (s *Service) onDownloadsVolume(c store.DownloadClient) bool {
	return c.Managed && s.cfg.ArrivalsRoot != ""
}

// noRoom is a release turned away for the room it needs on the downloads
// volume: it waits, and the request says why.
type noRoom struct{ msg string }

func (e noRoom) Error() string { return e.msg }

// admitDownloadsVolume holds a release the downloads volume cannot take whole.
func (s *Service) admitDownloadsVolume(c store.DownloadClient, sizeBytes int64, protocol string) error {
	free, err := s.freeSpace(s.downloadsPath(c))
	if err != nil {
		// Fails closed, like the floor: "could not check" is never "there is room".
		return fmt.Errorf("refusing grab: the downloads volume: %w", err)
	}
	reserve := s.cfg.DownloadsReserveBytes
	var need int64
	var msg string
	switch {
	case sizeBytes <= 0:
		need = reserve
		msg = fmt.Sprintf("the downloads volume keeps a %s reserve and has %s free", storage.Human(reserve), storage.Human(free))
	case protocol == "usenet":
		need = 2*sizeBytes + reserve
		msg = fmt.Sprintf("this NZB needs %s on the downloads volume (twice its %s, for the archive and its unpacking, and the %s reserve) and %s is free",
			storage.Human(need), storage.Human(sizeBytes), storage.Human(reserve), storage.Human(free))
	default:
		need = sizeBytes + reserve
		msg = fmt.Sprintf("this release needs %s on the downloads volume (its %s and the %s reserve) and %s is free",
			storage.Human(need), storage.Human(sizeBytes), storage.Human(reserve), storage.Human(free))
	}
	if free < need {
		return noRoom{msg: "waiting for room: " + msg}
	}
	return nil
}

// freeSpaceAt is the free space the filesystem reports at path.
func freeSpaceAt(path string) (int64, error) { return storage.FreeAt(path) }

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
