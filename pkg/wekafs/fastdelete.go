package wekafs

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/tigrawap/locar/pkg/engine"
	"github.com/tigrawap/locar/pkg/fsutil"
)

// The thread counts locar's own CLI defaults to, restated here because the library does not apply
// them. The entry types deliberately differ - see garbageCollectionEntryTypes.
//
// engine.Config is a plain struct with no defaulting: built from its zero value it walks with no
// threads, no result workers, and - because Types is empty - matches no entry at all, so a purge
// completes instantly, reports no error, and deletes nothing. The binary this replaced was getting
// these from flag defaults, so anything else here would be a silent change in how the trash is
// emptied rather than a like-for-like swap.
const (
	garbageCollectionDeleteThreads = 128
	garbageCollectionResultThreads = 128

	// garbageCollectionReaddirTimeout bounds one readdir, and is set explicitly rather than left to
	// locar's own 5 minute default because it is the only thing that bounds how long a purge can
	// outlive its cancellation.
	//
	// The engine checks the context between operations, but the deadline on an individual readdir
	// is a plain time.After race against the syscall and does not watch the context at all. So a
	// readdir already in flight when garbageCollectionTimeout expires still runs to this deadline,
	// holding the filesystem mount and the gc claim for that long past the budget it was given. At
	// locar's default that is half the purge budget again; a minute keeps the overrun small while
	// being orders of magnitude longer than a readdir on a healthy mount.
	//
	// It is not free either way: upstream documents that a timed-out readdir leaves its OS thread
	// hanging, and those accumulate in this process now rather than being reclaimed when a separate
	// locar process exited. Fewer, shorter waits is the better trade.
	garbageCollectionReaddirTimeout = time.Minute
)

// garbageCollectionEntryTypes deliberately says "all" rather than naming types.
//
// This is the one place the configuration does NOT copy locar's CLI defaults, because those
// defaults are wrong for a delete-everything pass. They select file, dir, link and socket; every
// other dirent type falls to a branch that collects the entry only when "all" was asked for, and
// otherwise just logs "Skipped record".
//
// Two cases make that matter, and the second is the reason this is not negotiable:
//
//  1. A FIFO, character or block device sitting directly in the trash root is skipped. It is not
//     stranded - the caller's RemoveAll of the trash directory still takes it - but the fast pass
//     silently leaves work behind for the single-threaded path it exists to avoid. Anything deeper
//     is collateral of its parent directory, since a delete-all deletes with RemoveAll.
//
//  2. DT_UNKNOWN. A filesystem that does not populate d_type in readdir reports it for every
//     entry, and the walker keys off that value twice: it only descends when the type is DT_DIR,
//     and it only collects a non-listed type under "all". With the CLI defaults that combination
//     collects nothing, descends nowhere, and deletes nothing - a complete no-op that reports
//     success and logs every entry as skipped. With "all" the top-level entries are collected and
//     RemoveAll takes each subtree whole, so the pass stays correct without d_type at all.
//
// Whether wekafs or an NFS mount fills d_type is not something to depend on, and it can differ by
// transport and by backend version. The binary this replaces ran with the same CLI defaults, so it
// carried the same gap.
var garbageCollectionEntryTypes = []string{"all"}

// entryCounter counts the entries locar reports while it works, by counting the newlines it writes.
//
// locar tracks totalDeleted and totalDeleteFailed internally but exports no accessor for them, so
// this is what a caller can observe without changing the library. It counts what locar *found and
// queued*, which for an empty-the-trash pass is the same set it deletes, but is not a confirmation
// that each one was removed - a failed unlink is invisible here. See the note on emptyDirectory.
type entryCounter struct {
	entries atomic.Int64
}

func (c *entryCounter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			c.entries.Add(1)
		}
	}
	return len(p), nil
}

func (c *entryCounter) count() int64 { return c.entries.Load() }

// emptyDirectory removes everything inside dir, in parallel, and reports how many entries locar
// accounted for. The directory itself is left in place - locar's delete-all only removes what it
// finds inside - so the caller still has to remove the now-empty directory.
//
// This replaces shelling out to a /locar binary baked into the image. As a library the version is
// pinned in go.mod and upgraded like any other dependency, rather than being a URL in two
// Dockerfiles that nothing checks; and there is no longer a silent fallback when the binary is
// missing from the image, which made a slow purge look like a fast one.
//
// A deletion that fails on an individual entry is not reported: locar counts it, but exposes no way
// to read that count, and its own CLI exits zero regardless. The caller's follow-up removal of the
// directory is what surfaces a genuinely failed purge, since a non-empty directory cannot be
// removed.
func emptyDirectory(ctx context.Context, dir string) (int64, error) {
	if err := fsutil.IsDir(dir); err != nil {
		return 0, err
	}

	counter := &entryCounter{}
	explorer, err := engine.New(ctx, engine.Config{
		DeleteAll:     true,
		Types:         garbageCollectionEntryTypes,
		Threads:       garbageCollectionDeleteThreads,
		ResultThreads: garbageCollectionResultThreads,
		Timeout:       garbageCollectionReaddirTimeout,
		Output:        counter,
		// StopOnError false: one unreadable subtree must not abandon the rest of the trash, which
		// would strand it until the next purge and grow without bound.
		StopOnError: false,
	})
	if err != nil {
		return 0, err
	}

	explorer.AddDir(dir)
	explorer.Start()
	// Wait blocks on the delete workers too, not just the walk, so the directory is genuinely empty
	// by the time this returns and the caller's removal cannot race it.
	explorer.Wait()

	// The engine takes the context but reports cancellation only through it, so this is the one
	// place a cut-short purge can be told from a complete one.
	if err := ctx.Err(); err != nil {
		return counter.count(), err
	}
	return counter.count(), nil
}
