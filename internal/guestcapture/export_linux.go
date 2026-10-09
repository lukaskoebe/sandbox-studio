//go:build linux

// Package guestcapture exports the guest's managed overlay upper layer.
package guestcapture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/upperlayer"
)

const (
	metadataPath       = "/.msb/root-disk.json"
	metadataLimit      = 1 << 20
	metadataSchema     = "microsandbox.runtime-root-disk/1"
	metadataLayout     = "managed-upper"
	metadataDeviceID   = "vdb"
	lockName           = ".studio-agent-capture.lock"
	lockWaitInterval   = 50 * time.Millisecond
	rootMountInfoLimit = 8 << 20
)

type rootDiskMetadata struct {
	Schema   string `json:"schema"`
	Layout   string `json:"layout"`
	DeviceID string `json:"device_id"`
}

type captureFiles struct {
	root          *os.File
	upper         *os.File
	rootIdentity  pinnedIdentity
	upperIdentity pinnedIdentity
	workIdentity  pinnedIdentity
}

func (f *captureFiles) close() {
	if f == nil {
		return
	}
	if f.upper != nil {
		_ = f.upper.Close()
	}
	if f.root != nil {
		_ = f.root.Close()
	}
}

type watchdog interface {
	waitReady(context.Context) error
	releaseAndWait(context.Context) error
	requestStop()
	wait() error
}

type exportDeps struct {
	lock       func(context.Context) (*os.File, error)
	discover   func() (*captureFiles, error)
	start      func(*os.File, *os.File) (watchdog, error)
	writeLayer func(context.Context, io.Writer, *os.File, ocilayer.Limits) error
}

var defaultExportDeps = exportDeps{
	lock:       acquireCaptureLock,
	discover:   discoverCaptureFiles,
	start:      startWatchdog,
	writeLayer: upperlayer.Write,
}

// Export sends a complete agentproto export stream containing a normalized
// tar archive of the guest's managed overlay upper directory.
func Export(ctx context.Context, dst io.Writer) error {
	return exportWith(ctx, dst, defaultExportDeps)
}

func exportWith(ctx context.Context, dst io.Writer, deps exportDeps) error {
	if ctx == nil {
		return errors.New("nil capture context")
	}
	if dst == nil {
		return errors.New("nil capture destination")
	}
	if deps.lock == nil || deps.discover == nil || deps.start == nil || deps.writeLayer == nil {
		return framePreflightError(ctx, dst, errors.New("incomplete guest capture dependencies"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	lockFile, err := deps.lock(ctx)
	if err != nil {
		if lockFile != nil {
			_ = lockFile.Close()
		}
		return framePreflightError(ctx, dst, fmt.Errorf("lock guest capture: %w", err))
	}
	if lockFile == nil {
		return framePreflightError(ctx, dst, errors.New("capture lock returned no file"))
	}
	// Closing the parent's descriptor releases its reference to the flock. The
	// watchdog inherits the same open-file-description and holds the lock if the
	// agent is killed before it can finish cleanup.
	defer lockFile.Close()

	files, err := deps.discover()
	if err != nil {
		return framePreflightError(ctx, dst, fmt.Errorf("discover managed upper layer: %w", err))
	}
	if files == nil || files.root == nil || files.upper == nil {
		if files != nil {
			files.close()
		}
		return framePreflightError(ctx, dst, errors.New("capture discovery returned incomplete pinned directories"))
	}
	defer files.close()

	wd, err := deps.start(files.root, lockFile)
	if err != nil {
		return framePreflightError(ctx, dst, fmt.Errorf("start freeze watchdog: %w", err))
	}
	if wd == nil {
		return framePreflightError(ctx, dst, errors.New("start freeze watchdog: runner returned nil"))
	}
	finished := false
	defer func() {
		if !finished {
			wd.requestStop()
			_ = wd.wait()
		}
	}()
	if err := wd.waitReady(ctx); err != nil {
		wd.requestStop()
		if waitErr := wd.wait(); waitErr != nil {
			finished = true
			return framePreflightError(ctx, dst, fmt.Errorf("freeze watchdog was not ready: %w (watchdog cleanup: %v)", err, waitErr))
		}
		finished = true
		return framePreflightError(ctx, dst, fmt.Errorf("freeze watchdog was not ready: %w", err))
	}
	if err := ctx.Err(); err != nil {
		wd.requestStop()
		waitErr := wd.wait()
		finished = true
		if waitErr != nil {
			return fmt.Errorf("capture canceled: %w (watchdog cleanup: %v)", err, waitErr)
		}
		return err
	}

	pipeReader, pipeWriter := io.Pipe()
	producerCtx, cancelProducer := context.WithCancel(ctx)
	defer cancelProducer()
	producerDone := make(chan error, 1)
	go func() {
		writeErr := deps.writeLayer(producerCtx, pipeWriter, files.upper, ocilayer.Limits{})
		if writeErr != nil {
			_ = pipeWriter.CloseWithError(writeErr)
			producerDone <- writeErr
			return
		}
		// upperlayer.Write returns after closing its raw tar writer. Keep the
		// pipe open until the helper confirms a normal release and completed thaw.
		if releaseErr := wd.releaseAndWait(producerCtx); releaseErr != nil {
			_ = pipeWriter.CloseWithError(releaseErr)
			producerDone <- releaseErr
			return
		}
		_ = pipeWriter.Close()
		producerDone <- nil
	}()

	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		_ = pipeReader.CloseWithError(ctx.Err())
		cancelProducer()
		wd.requestStop()
	})
	defer func() {
		if !stopCancel() {
			<-cancelDone
		}
	}()

	limits := ocilayer.DefaultLimits()
	if limits.MaxOutputBytes <= 0 {
		_ = pipeReader.CloseWithError(errors.New("invalid OCI export output limit"))
		cancelProducer()
		wd.requestStop()
		_ = wd.wait()
		producerErr := <-producerDone
		finished = true
		return errors.Join(errors.New("invalid OCI export output limit"), producerErr)
	}
	_, exportErr := agentproto.WriteExport(ctx, dst, pipeReader, limits.MaxOutputBytes)
	_ = pipeReader.Close()
	if exportErr != nil {
		cancelProducer()
		_ = pipeReader.CloseWithError(exportErr)
		wd.requestStop()
	}
	producerErr := <-producerDone
	if exportErr != nil {
		waitErr := wd.wait()
		finished = true
		if waitErr != nil {
			return fmt.Errorf("write guest export: %w (watchdog cleanup: %v)", exportErr, waitErr)
		}
		return fmt.Errorf("write guest export: %w", exportErr)
	}
	if producerErr != nil {
		wd.requestStop()
		waitErr := wd.wait()
		finished = true
		if waitErr != nil {
			return fmt.Errorf("guest layer producer failed: %w (watchdog cleanup: %v)", producerErr, waitErr)
		}
		return fmt.Errorf("guest layer producer failed: %w", producerErr)
	}
	if err := wd.wait(); err != nil {
		finished = true
		return fmt.Errorf("freeze watchdog exit: %w", err)
	}
	finished = true

	return nil
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func framePreflightError(ctx context.Context, dst io.Writer, cause error) error {
	if ctx == nil || dst == nil || ctx.Err() != nil {
		return cause
	}
	limit := ocilayer.DefaultLimits().MaxOutputBytes
	if limit <= 0 {
		return cause
	}
	_, err := agentproto.WriteExport(ctx, dst, errorReader{err: cause}, limit)
	if err == nil {
		return cause
	}
	return err
}

func acquireCaptureLock(ctx context.Context) (*os.File, error) {
	shm, err := openVerifiedShm()
	if err != nil {
		return nil, err
	}
	defer shm.Close()

	fd, err := unix.Openat(int(shm.Fd()), lockName,
		unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open /dev/shm capture lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), "/dev/shm/"+lockName)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		lock.Close()
		return nil, fmt.Errorf("stat /dev/shm capture lock: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Mode&0o077 != 0 {
		lock.Close()
		return nil, errors.New("/dev/shm capture lock has unsafe owner, type, links, or permissions")
	}
	var shmStat unix.Stat_t
	if err := unix.Fstat(int(shm.Fd()), &shmStat); err != nil || st.Dev != shmStat.Dev {
		lock.Close()
		if err != nil {
			return nil, fmt.Errorf("stat verified /dev/shm directory: %w", err)
		}
		return nil, errors.New("capture lock is not on the verified /dev/shm filesystem")
	}

	for {
		if err := ctx.Err(); err != nil {
			lock.Close()
			return nil, err
		}
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN && err != unix.EINTR {
			lock.Close()
			return nil, fmt.Errorf("lock /dev/shm capture lock: %w", err)
		}
		timer := time.NewTimer(lockWaitInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			lock.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func openVerifiedShm() (*os.File, error) {
	fd, err := unix.Open("/dev/shm", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/shm: %w", err)
	}
	shm := os.NewFile(uintptr(fd), "/dev/shm")
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		shm.Close()
		return nil, fmt.Errorf("inspect /dev/shm filesystem: %w", err)
	}
	if uint64(fs.Type) != uint64(unix.TMPFS_MAGIC) {
		shm.Close()
		return nil, errors.New("/dev/shm is not tmpfs")
	}
	if err := verifyShmMountInfo(); err != nil {
		shm.Close()
		return nil, err
	}
	return shm, nil
}

func verifyShmMountInfo() error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("open mountinfo: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(io.LimitReader(f, rootMountInfoLimit+1))
	scanner.Buffer(make([]byte, 4096), rootMountInfoLimit)
	found := false
	for scanner.Scan() {
		left, right, ok := strings.Cut(scanner.Text(), " - ")
		if !ok {
			continue
		}
		fields, tail := strings.Fields(left), strings.Fields(right)
		if len(fields) < 5 || len(tail) < 1 || unescapeMountField(fields[4]) != "/dev/shm" {
			continue
		}
		if tail[0] != "tmpfs" {
			return fmt.Errorf("/dev/shm mount is %q, want tmpfs", tail[0])
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read mountinfo: %w", err)
	}
	if !found {
		return errors.New("/dev/shm is not a distinct tmpfs mount")
	}
	return nil
}

func discoverCaptureFiles() (*captureFiles, error) {
	metadata, err := readRootDiskMetadata()
	if err != nil {
		return nil, err
	}
	if err := verifyRootOverlay(); err != nil {
		return nil, err
	}
	device, rdev, err := openExpectedBlockDevice(metadata.DeviceID)
	if err != nil {
		return nil, err
	}
	defer device.Close()

	files, err := discoverPinnedUpper(rdev)
	if err != nil {
		return nil, err
	}
	if err := verifyPinnedFilesystem(int(device.Fd()), files); err != nil {
		files.close()
		return nil, err
	}
	return files, nil
}

func readRootDiskMetadata() (rootDiskMetadata, error) {
	var metadata rootDiskMetadata
	rootfd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return metadata, fmt.Errorf("open guest root: %w", err)
	}
	defer unix.Close(rootfd)
	msbfd, err := unix.Openat(rootfd, ".msb", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return metadata, fmt.Errorf("open guest metadata directory: %w", err)
	}
	defer unix.Close(msbfd)
	fd, err := unix.Openat(msbfd, "root-disk.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return metadata, fmt.Errorf("open guest root metadata: %w", err)
	}
	file := os.NewFile(uintptr(fd), metadataPath)
	defer file.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return metadata, fmt.Errorf("stat guest root metadata: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return metadata, errors.New("guest root metadata is not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, metadataLimit+1))
	if err != nil {
		return metadata, fmt.Errorf("read guest root metadata: %w", err)
	}
	if len(raw) == 0 || len(raw) > metadataLimit {
		return metadata, errors.New("guest root metadata is empty or too large")
	}
	if err := validateMetadataJSON(raw, &metadata); err != nil {
		return metadata, fmt.Errorf("parse guest root metadata: %w", err)
	}
	if metadata.Schema != metadataSchema {
		return metadata, fmt.Errorf("unsupported guest root metadata schema %q", metadata.Schema)
	}
	if metadata.Layout != metadataLayout {
		return metadata, fmt.Errorf("guest root layout %q is not managed-upper", metadata.Layout)
	}
	if metadata.DeviceID != metadataDeviceID {
		return metadata, fmt.Errorf("expected guest root device_id %q", metadataDeviceID)
	}
	return metadata, nil
}

func openExpectedBlockDevice(deviceID string) (*os.File, uint64, error) {
	if deviceID != metadataDeviceID {
		return nil, 0, fmt.Errorf("refusing unexpected guest device_id %q", deviceID)
	}
	path := "/dev/" + metadataDeviceID
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open guest root block device: %w", err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, 0, fmt.Errorf("stat guest root block device: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFBLK || st.Rdev == 0 {
		unix.Close(fd)
		return nil, 0, errors.New("/dev/vdb is not a usable block device")
	}
	return os.NewFile(uintptr(fd), path), uint64(st.Rdev), nil
}

type pinnedIdentity struct {
	dev uint64
	ino uint64
}

func discoverPinnedUpper(rdev uint64) (*captureFiles, error) {
	entries, err := os.ReadDir("/proc/1/fd")
	if err != nil {
		return nil, fmt.Errorf("enumerate guest init fds: %w", err)
	}
	seen := make(map[pinnedIdentity]struct{})
	candidates := make([]*captureFiles, 0, 2)
	for _, entry := range entries {
		path := "/proc/1/fd/" + entry.Name()
		var observed unix.Stat_t
		if err := unix.Stat(path, &observed); err != nil || uint64(observed.Dev) != rdev || observed.Mode&unix.S_IFMT != unix.S_IFDIR {
			continue
		}
		rootFD, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		var rootStat unix.Stat_t
		if err := unix.Fstat(rootFD, &rootStat); err != nil || uint64(rootStat.Dev) != rdev {
			unix.Close(rootFD)
			continue
		}
		identity := pinnedIdentity{dev: uint64(rootStat.Dev), ino: rootStat.Ino}
		if _, ok := seen[identity]; ok {
			unix.Close(rootFD)
			continue
		}
		seen[identity] = struct{}{}

		upperFD, err := unix.Openat(rootFD, "upper", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NOATIME, 0)
		if err != nil {
			unix.Close(rootFD)
			continue
		}
		workFD, err := unix.Openat(rootFD, "work", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NOATIME, 0)
		if err != nil {
			unix.Close(upperFD)
			unix.Close(rootFD)
			continue
		}
		var upperStat, workStat unix.Stat_t
		upperErr, workErr := unix.Fstat(upperFD, &upperStat), unix.Fstat(workFD, &workStat)
		var rootFS, upperFS, workFS unix.Statfs_t
		rootFSErr, upperFSErr, workFSErr := unix.Fstatfs(rootFD, &rootFS), unix.Fstatfs(upperFD, &upperFS), unix.Fstatfs(workFD, &workFS)
		unix.Close(workFD)
		if upperErr != nil || workErr != nil || rootFSErr != nil || upperFSErr != nil || workFSErr != nil || uint64(rootStat.Dev) != rdev || uint64(upperStat.Dev) != rdev || uint64(workStat.Dev) != rdev || upperStat.Mode&unix.S_IFMT != unix.S_IFDIR || workStat.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(rootFS.Type) != uint64(unix.EXT4_SUPER_MAGIC) || uint64(upperFS.Type) != uint64(unix.EXT4_SUPER_MAGIC) || uint64(workFS.Type) != uint64(unix.EXT4_SUPER_MAGIC) {
			unix.Close(upperFD)
			unix.Close(rootFD)
			continue
		}
		candidates = append(candidates, &captureFiles{
			root:          os.NewFile(uintptr(rootFD), path),
			upper:         os.NewFile(uintptr(upperFD), path+"/upper"),
			rootIdentity:  pinnedIdentity{dev: uint64(rootStat.Dev), ino: rootStat.Ino},
			upperIdentity: pinnedIdentity{dev: uint64(upperStat.Dev), ino: upperStat.Ino},
			workIdentity:  pinnedIdentity{dev: uint64(workStat.Dev), ino: workStat.Ino},
		})
	}
	if len(candidates) != 1 {
		for _, candidate := range candidates {
			candidate.close()
		}
		return nil, fmt.Errorf("expected exactly one pinned upperfs root on /dev/vdb, found %d", len(candidates))
	}
	return candidates[0], nil
}

func verifyRootOverlay() error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("open mountinfo: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, rootMountInfoLimit+1))
	scanner.Buffer(make([]byte, 4096), rootMountInfoLimit)
	var found bool
	for scanner.Scan() {
		left, right, ok := strings.Cut(scanner.Text(), " - ")
		if !ok {
			continue
		}
		fields, tail := strings.Fields(left), strings.Fields(right)
		if len(fields) < 6 || len(tail) < 3 || unescapeMountField(fields[4]) != "/" {
			continue
		}
		if found {
			return errors.New("guest root mount is ambiguous in mountinfo")
		}
		found = true
		if tail[0] != "overlay" {
			return fmt.Errorf("guest root filesystem is %q, want overlay", tail[0])
		}
		if !hasMountOption(fields[5], "rw") || hasMountOption(fields[5], "ro") {
			return errors.New("guest root overlay is not mounted read-write")
		}
		options := make(map[string]string)
		for _, option := range strings.Split(tail[2], ",") {
			key, value, hasValue := strings.Cut(option, "=")
			if hasValue {
				options[key] = unescapeMountField(value)
			} else {
				options[key] = ""
			}
		}
		lowerDir, upperDir, workDir := options["lowerdir"], options["upperdir"], options["workdir"]
		if lowerDir == "" || upperDir == "" || workDir == "" {
			return errors.New("guest root overlay lacks lowerdir, upperdir, or workdir")
		}
		if filepath.Clean(lowerDir) != "/.msb/rootfs/lower" || filepath.Clean(upperDir) != "/.msb/rootfs/upperfs/upper" || filepath.Clean(workDir) != "/.msb/rootfs/upperfs/work" {
			return errors.New("guest root overlay does not use the managed upperfs upper/work pair")
		}
		for _, unsupported := range []string{"metacopy", "redirect_dir", "index", "xino", "nfs_export", "userxattr", "volatile", "verity"} {
			enabled, ok := overlayOptionEnabled(options, unsupported)
			if ok && enabled {
				return fmt.Errorf("guest root overlay enables unsupported option %q", unsupported)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read mountinfo: %w", err)
	}
	if !found {
		return errors.New("guest root mount was not found in mountinfo")
	}
	return nil
}

func overlayOptionEnabled(options map[string]string, name string) (bool, bool) {
	value, ok := options[name]
	if !ok {
		return false, false
	}
	switch strings.ToLower(value) {
	case "off", "n", "no", "0", "false":
		return false, true
	case "", "on", "y", "yes", "1", "true":
		return true, true
	default:
		// Unknown modes may enable semantics the layer serializer cannot retain.
		return true, true
	}
}

func hasMountOption(options, want string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == want {
			return true
		}
	}
	return false
}

func unescapeMountField(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

func verifyPinnedFilesystem(deviceFD int, files *captureFiles) error {
	var st unix.Stat_t
	if err := unix.Fstat(deviceFD, &st); err != nil {
		return fmt.Errorf("stat root block device: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFBLK || uint64(st.Rdev) != files.rootIdentity.dev || uint64(st.Rdev) != files.upperIdentity.dev || uint64(st.Rdev) != files.workIdentity.dev {
		return errors.New("/dev/vdb does not match the pinned upperfs root/upper/work device")
	}
	rootStat, err := fileStat(files.root)
	if err != nil {
		return fmt.Errorf("stat pinned upperfs root: %w", err)
	}
	upperStat, err := fileStat(files.upper)
	if err != nil {
		return fmt.Errorf("stat pinned upper directory: %w", err)
	}
	if uint64(rootStat.Dev) != uint64(st.Rdev) || uint64(upperStat.Dev) != uint64(st.Rdev) || rootStat.Ino != files.rootIdentity.ino || upperStat.Ino != files.upperIdentity.ino || files.workIdentity.ino == 0 || files.upperIdentity.ino == files.workIdentity.ino {
		return errors.New("pinned upperfs root/upper/work identity changed during discovery")
	}
	for label, file := range map[string]*os.File{"pinned root": files.root, "upper directory": files.upper} {
		var fs unix.Statfs_t
		if err := unix.Fstatfs(int(file.Fd()), &fs); err != nil {
			return fmt.Errorf("inspect %s filesystem: %w", label, err)
		}
		if uint64(fs.Type) != uint64(unix.EXT4_SUPER_MAGIC) {
			return fmt.Errorf("%s filesystem magic %#x is not ext4", label, fs.Type)
		}
	}
	return nil
}

func fileStat(file *os.File) (unix.Stat_t, error) {
	var st unix.Stat_t
	if file == nil {
		return st, errors.New("nil pinned file")
	}
	err := unix.Fstat(int(file.Fd()), &st)
	return st, err
}

// ensure the metadata decoder is strict about additional trailing JSON values.
func validateMetadataJSON(raw []byte, metadata *rootDiskMetadata) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(metadata); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("guest root metadata has multiple JSON values")
		}
		return err
	}
	return nil
}
