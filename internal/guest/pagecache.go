//go:build linux

package guest

import (
	"bytes"
	"io"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// A copy streams every file of a tree through the guest's page cache, and a VM's host process
// keeps each page of guest memory resident once the guest has touched it. Copying a tree larger
// than a VM's memory so grows the VM to its whole memory size on the host, which a rebase does
// to two VMs at once, and getting the pages back afterwards is slow and partial. So an export and
// an import drop the cache of the files they copy as they go, and the guest reuses the same few
// pages instead of touching all of its memory.

// cacheChunk is how much of the files copied may stay in the cache before it is dropped.
const cacheChunk = 32 << 20

// copyOut copies size bytes of f to w. A file none of which was in the cache before is dropped
// from it every cacheChunk and at the end; a file that was, as a running sandbox's working
// files may be, keeps its pages.
func copyOut(w io.Writer, f *os.File, size int64) (int64, error) {
	fd := int(f.Fd())
	if size == 0 || cached(fd) {
		return io.CopyN(w, f, size)
	}
	var n int64
	for n < size {
		k, err := io.CopyN(w, f, min(cacheChunk, size-n))
		n += k
		_ = unix.Fadvise(fd, 0, 0, unix.FADV_DONTNEED)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// cached reports whether any page of the file fd is in the page cache. It reports true when the
// kernel can't tell, so that nothing is dropped.
func cached(fd int) bool {
	var st unix.Cachestat_t
	if err := unix.Cachestat(uint(fd), &unix.CachestatRange{}, &st, 0); err != nil {
		return true
	}
	return st.Cache > 0
}

// cacheDropper drops the page cache of the files an import writes. They are new, so nothing
// else reads them yet. A written page can only be dropped once it is on the disk: writeback of
// each file starts when it is written, and once the files written since the last drop hold
// cacheChunk bytes, or there are maxPending of them, the dropper waits for it and drops them.
type cacheDropper struct {
	pending []int // duplicates of the written files' descriptors
	bytes   int64
}

// maxPending caps the files that wait to be dropped, each with a descriptor open.
const maxPending = 256

// writer returns a writer to the new file f that drops what it wrote every cacheChunk, for a
// file larger than that, and adds what it writes to copied.
func (*cacheDropper) writer(f *os.File, copied *atomic.Int64) *droppingWriter {
	return &droppingWriter{f: f, fd: int(f.Fd()), copied: copied}
}

// written queues the file fd, now fully written, to be dropped. It keeps its own descriptor
// of the file; the caller still closes fd.
func (d *cacheDropper) written(fd int, size int64) {
	if size == 0 {
		return
	}
	_ = unix.SyncFileRange(fd, 0, 0, unix.SYNC_FILE_RANGE_WRITE)
	dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return
	}
	d.pending = append(d.pending, dup)
	d.bytes += size
	if d.bytes >= cacheChunk || len(d.pending) >= maxPending {
		d.drop()
	}
}

// drop waits for the queued files to be written and drops them from the cache.
func (d *cacheDropper) drop() {
	for _, fd := range d.pending {
		dropWritten(fd, 0, 0)
		unix.Close(fd)
	}
	d.pending, d.bytes = d.pending[:0], 0
}

// dropWritten waits for the length bytes of fd at off to be written (all of it for 0) and drops
// them from the cache.
func dropWritten(fd int, off, length int64) {
	_ = unix.SyncFileRange(fd, off, length, unix.SYNC_FILE_RANGE_WAIT_BEFORE|unix.SYNC_FILE_RANGE_WRITE|unix.SYNC_FILE_RANGE_WAIT_AFTER)
	_ = unix.Fadvise(fd, off, length, unix.FADV_DONTNEED)
}

// droppingWriter writes a new file and drops each cacheChunk of it it wrote. A write of nothing
// but zeros moves past them instead: an archive holds a sparse file's holes as zeros, and
// writing them can take a disk that the file fits on as it was.
type droppingWriter struct {
	f       *os.File
	fd      int
	n, done int64 // written, and dropped up to
	hole    bool  // the last write moved past zeros
	copied  *atomic.Int64
}

func (w *droppingWriter) Write(p []byte) (n int, err error) {
	w.hole = allZero(p)
	if w.hole {
		if _, err = w.f.Seek(int64(len(p)), io.SeekCurrent); err == nil {
			n = len(p)
		}
	} else {
		n, err = w.f.Write(p)
	}
	w.n += int64(n)
	w.copied.Add(int64(n))
	if w.n-w.done >= cacheChunk {
		dropWritten(w.fd, w.done, w.n-w.done)
		w.done = w.n
	}
	return n, err
}

// finish gives a file that ends in a hole its size.
func (w *droppingWriter) finish() error {
	if !w.hole {
		return nil
	}
	return w.f.Truncate(w.n)
}

func allZero(p []byte) bool {
	return len(p) > 0 && p[0] == 0 && bytes.Equal(p[1:], p[:len(p)-1])
}
