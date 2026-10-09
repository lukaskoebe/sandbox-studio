#!/usr/bin/env python3
"""Disposable in-guest freeze/lifecycle probe; intentionally not a tar exporter."""

from __future__ import annotations

import argparse
import errno
import fcntl
import hashlib
import json
import os
import select
import signal
import socket
import stat
import struct
import sys
import tempfile
import time
import traceback

FREEZE = 0xC0045877
THAW = 0xC0045878
SCHEMA = "microsandbox.runtime-root-disk/1"
LAYOUT = "managed-upper"
EXPECTED_DEVICE_ID = "vdb"
BLOCK_SIZE = 4096
WATCHDOG_SECONDS = 3.0
READY_SECONDS = 8.0
IOCTL_FIFREEZE = FREEZE
IOCTL_FITHAW = THAW
O_CLOEXEC = getattr(os, "O_CLOEXEC", 0)
O_NOFOLLOW = getattr(os, "O_NOFOLLOW", 0)
O_NOATIME = getattr(os, "O_NOATIME", 0)


class ProbeError(RuntimeError):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ProbeError(message)


def write_all(fd: int, data: bytes) -> None:
    view = memoryview(data)
    while view:
        count = os.write(fd, view)
        if count <= 0:
            raise ProbeError("short write")
        view = view[count:]


def write_json_atomic(path: str, value: dict) -> None:
    temp = path + ".tmp"
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | O_CLOEXEC, 0o600)
    try:
        write_all(fd, (json.dumps(value, separators=(",", ":")) + "\n").encode())
    finally:
        os.close(fd)
    os.replace(temp, path)


def send_event(sock: socket.socket, value: dict) -> None:
    sock.sendall((json.dumps(value, separators=(",", ":")) + "\n").encode())


def recv_event(sock: socket.socket, timeout: float) -> dict | None:
    deadline = time.monotonic() + timeout
    line = bytearray()
    while time.monotonic() < deadline:
        ready, _, _ = select.select([sock], [], [], max(0.0, deadline - time.monotonic()))
        if not ready:
            return None
        chunk = sock.recv(1)
        if not chunk:
            return None
        if chunk == b"\n":
            try:
                value = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ProbeError(f"invalid watchdog event: {exc}") from exc
            if not isinstance(value, dict):
                raise ProbeError("watchdog event is not an object")
            return value
        line.extend(chunk)
        require(len(line) <= 4096, "watchdog event too large")
    return None


def require_dev_shm_tmpfs() -> None:
    def unescape_mount_field(value: str) -> str:
        return (value.replace("\\040", " ").replace("\\011", "\t")
                .replace("\\012", "\n").replace("\\134", "\\"))

    try:
        with open("/proc/self/mountinfo", "r", encoding="utf-8") as mountinfo:
            for line in mountinfo:
                left, sep, right = line.partition(" - ")
                if not sep:
                    continue
                fields = left.split()
                tail = right.split()
                if len(fields) >= 5 and tail and unescape_mount_field(fields[4]) == "/dev/shm":
                    require(tail[0] == "tmpfs", "/dev/shm is not a tmpfs mount")
                    return
    except OSError as exc:
        raise ProbeError(f"cannot inspect /dev/shm mount: {exc}") from exc
    raise ProbeError("/dev/shm is not a distinct tmpfs mount")


def read_guest_metadata() -> dict:
    path = "/.msb/root-disk.json"
    try:
        with open(path, "rb") as source:
            raw = source.read(1024 * 1024 + 1)
    except OSError as exc:
        raise ProbeError(f"cannot read guest root metadata {path}: {exc}") from exc
    require(0 < len(raw) <= 1024 * 1024, "guest root metadata is empty or too large")
    try:
        metadata = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ProbeError(f"guest root metadata is invalid JSON: {exc}") from exc
    require(isinstance(metadata, dict), "guest root metadata must be an object")
    require(metadata.get("schema") == SCHEMA, "unsupported or missing root metadata schema")
    require(metadata.get("layout") == LAYOUT, "root metadata is not managed-upper")
    device_id = metadata.get("device_id")
    require(device_id == EXPECTED_DEVICE_ID, f"expected guest device_id {EXPECTED_DEVICE_ID!r}")
    return metadata


def expected_device_rdev(metadata: dict) -> int:
    device_path = "/dev/" + metadata["device_id"]
    try:
        device_stat = os.stat(device_path)
    except OSError as exc:
        raise ProbeError(f"cannot stat guest root device {device_path}: {exc}") from exc
    require(stat.S_ISBLK(device_stat.st_mode), f"{device_path} is not a block device")
    require(device_stat.st_rdev != 0, f"{device_path} has an invalid device number")
    return device_stat.st_rdev


def open_pin(rdev: int) -> tuple[int, int]:
    candidates: dict[tuple[int, int], tuple[int, str]] = {}
    try:
        with os.scandir("/proc/1/fd") as scanned:
            entries = sorted(scanned, key=lambda entry: entry.name)
    except OSError as exc:
        raise ProbeError(f"cannot enumerate guest init fds: {exc}") from exc
    try:
        for entry in entries:
            rootfd = upperfd = workfd = None
            try:
                observed = os.stat(entry.path)
                if observed.st_dev != rdev:
                    continue
                # Following this guest /proc fd symlink opens the already-pinned mount.
                rootfd = os.open(entry.path, os.O_RDONLY | os.O_DIRECTORY | O_CLOEXEC)
                root_stat = os.fstat(rootfd)
                if root_stat.st_dev != rdev:
                    continue
                upperfd = os.open("upper", os.O_RDONLY | os.O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW,
                                  dir_fd=rootfd)
                workfd = os.open("work", os.O_RDONLY | os.O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW,
                                 dir_fd=rootfd)
                upper_stat = os.fstat(upperfd)
                work_stat = os.fstat(workfd)
                if upper_stat.st_dev != rdev or work_stat.st_dev != rdev:
                    continue
                identity = (root_stat.st_dev, root_stat.st_ino)
                if identity in candidates:
                    continue
                candidates[identity] = (rootfd, entry.path)
                rootfd = None
            except OSError:
                pass
            finally:
                for fd in (upperfd, workfd, rootfd):
                    if fd is not None:
                        try:
                            os.close(fd)
                        except OSError:
                            pass
    except BaseException:
        for candidate_fd, _ in candidates.values():
            try:
                os.close(candidate_fd)
            except OSError:
                pass
        raise
    if len(candidates) != 1:
        for candidate_fd, _ in candidates.values():
            try:
                os.close(candidate_fd)
            except OSError:
                pass
        raise ProbeError(f"expected one pinned upperfs root, found {len(candidates)}")
    rootfd, _guest_proc_fd_path = next(iter(candidates.values()))
    upperfd = None
    try:
        upperfd = os.open("upper", os.O_RDONLY | os.O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW,
                          dir_fd=rootfd)
        require(os.fstat(rootfd).st_dev == rdev, "pinned root st_dev does not match guest device")
        require(os.fstat(upperfd).st_dev == rdev, "pinned upper st_dev does not match guest device")
        return rootfd, upperfd
    except BaseException:
        if upperfd is not None:
            try:
                os.close(upperfd)
            except OSError:
                pass
        os.close(rootfd)
        raise


def open_beneath(rootfd: int, relative_path: str, flags: int) -> int:
    parts = relative_path.split("/")
    require(parts and all(part not in ("", ".", "..") for part in parts),
            "unsafe upper-relative fixture path")
    parentfd = os.dup(rootfd)
    try:
        for part in parts[:-1]:
            nextfd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW,
                             dir_fd=parentfd)
            os.close(parentfd)
            parentfd = nextfd
        return os.open(parts[-1], flags | O_CLOEXEC | O_NOFOLLOW, dir_fd=parentfd)
    finally:
        os.close(parentfd)


def make_record(counter: int) -> bytes:
    header = struct.pack("!Q", counter)
    payload = bytes([counter % 251]) * (BLOCK_SIZE - 40)
    return header + hashlib.sha256(header + payload).digest() + payload


def validate_record(data: bytes) -> int:
    require(len(data) == BLOCK_SIZE, "upper fixture record has the wrong size")
    counter = struct.unpack("!Q", data[:8])[0]
    require(hashlib.sha256(data[:8] + data[40:]).digest() == data[8:40],
            "upper fixture record checksum mismatch")
    require(data[40:] == bytes([counter % 251]) * (BLOCK_SIZE - 40),
            "upper fixture record payload mismatch")
    return counter


def read_upper_record(upperfd: int, relative_path: str) -> bytes:
    require(O_NOATIME != 0, "O_NOATIME is unavailable; refusing an atime-writing read")
    fd = open_beneath(upperfd, relative_path, os.O_RDONLY | O_NOATIME)
    try:
        require(os.fstat(fd).st_dev == os.fstat(upperfd).st_dev,
                "fixture is not on the pinned upper filesystem")
        chunks = bytearray()
        while len(chunks) < BLOCK_SIZE:
            part = os.read(fd, BLOCK_SIZE - len(chunks))
            if not part:
                break
            chunks.extend(part)
        data = bytes(chunks)
        validate_record(data)
        return data
    finally:
        os.close(fd)


def write_progress(path: str, counter: int) -> None:
    temp = path + f".{os.getpid()}.tmp"
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | O_CLOEXEC, 0o600)
    try:
        write_all(fd, f"{counter}\n".encode())
    finally:
        os.close(fd)
    os.replace(temp, path)


def read_progress(path: str) -> int:
    try:
        with open(path, "r", encoding="ascii") as source:
            return int(source.read().strip())
    except (OSError, ValueError) as exc:
        raise ProbeError(f"cannot read writer progress: {exc}") from exc


def writer_main(data_path: str, progress_path: str) -> int:
    fd = os.open(data_path, os.O_WRONLY | O_CLOEXEC | O_NOFOLLOW)
    try:
        counter = 0
        while True:
            data = make_record(counter)
            offset = 0
            while offset < len(data):
                count = os.pwrite(fd, data[offset:], offset)
                if count <= 0:
                    raise ProbeError("short fixture write")
                offset += count
            os.fsync(fd)
            write_progress(progress_path, counter)
            counter += 1
            time.sleep(0.01)
    finally:
        os.close(fd)


def worker_main(command_sock: socket.socket, owner_sock: socket.socket) -> int:
    try:
        command = command_sock.recv(1)
        if command == b"R":
            owner_sock.sendall(b"R")
        elif command == b"C":
            owner_sock.sendall(b"C")
        elif command == b"T":
            ready, _, _ = select.select([command_sock], [], [], WATCHDOG_SECONDS + 2.0)
            if ready:
                command_sock.recv(1)  # Parent tells the lease holder to close after timeout.
        elif command == b"":  # Parent EOF: close the watchdog lease without a release byte.
            pass
        else:
            raise ProbeError(f"invalid controller command {command!r}")
        return 0
    except BaseException:
        traceback.print_exc()
        return 1
    finally:
        command_sock.close()
        owner_sock.close()


def watchdog_main(rootfd: int, owner_sock: socket.socket, status_sock: socket.socket,
                  result_path: str, timeout: float) -> int:
    frozen = False
    thawed = False
    event: dict = {"event": "error", "reason": "watchdog-error", "thawed": False}
    exit_code = 1
    try:
        os.setsid()
        fcntl.ioctl(rootfd, IOCTL_FIFREEZE, 0)
        frozen = True
        send_event(status_sock, {"event": "ready"})
        deadline = time.monotonic() + timeout
        ready, _, _ = select.select([owner_sock], [], [], timeout)
        if not ready or time.monotonic() >= deadline:
            reason = "timeout"
        else:
            command = owner_sock.recv(1)
            if command == b"R":
                reason = "release"
            elif command == b"C":
                reason = "cancel"
            elif command == b"":
                reason = "owner-gone"
            else:
                reason = "invalid-command"
        fcntl.ioctl(rootfd, IOCTL_FITHAW, 0)
        frozen = False
        thawed = True
        event = {"event": "thawed", "reason": reason, "thawed": True}
        exit_code = 0
    except BaseException as exc:
        event = {"event": "error", "reason": "watchdog-error",
                 "error": f"{type(exc).__name__}: {exc}", "thawed": False}
        if frozen:
            try:
                fcntl.ioctl(rootfd, IOCTL_FITHAW, 0)
                frozen = False
                thawed = True
                event["thawed"] = True
            except OSError as thaw_exc:
                event["thaw_error"] = f"{type(thaw_exc).__name__}: {thaw_exc}"
        exit_code = 1
    finally:
        try:
            event["thawed"] = event.get("thawed", False) or thawed
            write_json_atomic(result_path, event)
        except BaseException:
            traceback.print_exc()
            exit_code = 1
        try:
            send_event(status_sock, event)
        except BaseException:
            pass
        owner_sock.close()
        status_sock.close()
        os.close(rootfd)
    return exit_code


def wait_child(pid: int, timeout: float, statuses: dict[int, int]) -> int:
    if pid in statuses:
        return statuses[pid]
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            got, status = os.waitpid(pid, os.WNOHANG)
        except ChildProcessError as exc:
            raise ProbeError(f"child {pid} was not waitable") from exc
        if got == pid:
            statuses[pid] = status
            return status
        time.sleep(0.02)
    raise ProbeError(f"timed out waiting for child {pid}")


def stop_child(pid: int | None, statuses: dict[int, int], errors: list[str]) -> None:
    if pid is None or pid <= 1 or pid in statuses:
        return
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        wait_child(pid, 0.8, statuses)
        return
    except ProbeError:
        pass
    try:
        os.kill(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    try:
        wait_child(pid, 1.5, statuses)
    except ProbeError as exc:
        errors.append(str(exc))


def emergency_thaw(rootfd: int, statuses: dict[int, int], errors: list[str]) -> bool:
    pid = os.fork()
    if pid == 0:
        exit_code = 1
        try:
            fcntl.ioctl(rootfd, IOCTL_FITHAW, 0)
            exit_code = 0
        except OSError as exc:
            # EINVAL means no freeze remains to thaw; the caller still invalidates the window.
            exit_code = 0 if exc.errno == errno.EINVAL else 1
        except BaseException:
            traceback.print_exc()
        os._exit(exit_code)
    try:
        status = wait_child(pid, 1.0, statuses)
    except ProbeError:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        try:
            status = wait_child(pid, 1.0, statuses)
        except ProbeError as exc:
            errors.append(f"bounded emergency thaw did not finish: {exc}")
            return False
    return os.waitstatus_to_exitcode(status) == 0


def wait_for_progress(path: str, after: int, timeout: float) -> int:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = read_progress(path)
        if value > after:
            return value
        time.sleep(0.02)
    raise ProbeError(f"writer progress did not advance beyond {after}")


def cleanup_fixture(fixture_dir: str | None, data_path: str | None, errors: list[str]) -> None:
    if fixture_dir is None:
        return
    try:
        if data_path and os.path.exists(data_path):
            os.unlink(data_path)
        os.rmdir(fixture_dir)
    except OSError as exc:
        errors.append(f"fixture cleanup failed: {exc}")


def cleanup_scratch(scratch_dir: str | None, errors: list[str]) -> None:
    if scratch_dir is None:
        return
    try:
        for name in os.listdir(scratch_dir):
            path = os.path.join(scratch_dir, name)
            if os.path.isfile(path) or os.path.islink(path):
                os.unlink(path)
            else:
                os.rmdir(path)
        os.rmdir(scratch_dir)
    except OSError as exc:
        errors.append(f"/dev/shm cleanup failed: {exc}")


def run_probe(mode: str) -> int:
    scratch_dir = fixture_dir = data_path = None
    rootfd = upperfd = None
    writer_pid = watchdog_pid = worker_pid = None
    controller_sock = watchdog_status = watchdog_owner = worker_end = None
    statuses: dict[int, int] = {}
    cleanup_errors: list[str] = []
    primary_error: BaseException | None = None
    freeze_attempted = False
    thaw_confirmed = False
    capture_candidate = False
    writer_resumed = False
    watchdog_reason = None
    scenario_passed = False
    progress_path = None
    result_path = None

    try:
        require_dev_shm_tmpfs()
        metadata = read_guest_metadata()
        rdev = expected_device_rdev(metadata)
        rootfd, upperfd = open_pin(rdev)

        scratch_dir = tempfile.mkdtemp(prefix="layer-freeze-", dir="/dev/shm")
        progress_path = os.path.join(scratch_dir, "writer-progress")
        result_path = os.path.join(scratch_dir, "watchdog-result.json")
        fixture_dir = tempfile.mkdtemp(prefix=f".layer-freeze-{os.getpid()}-",
                                       dir="/var/tmp")
        data_path = os.path.join(fixture_dir, "record")
        fd = os.open(data_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | O_CLOEXEC, 0o600)
        try:
            write_all(fd, make_record(0))
            os.fsync(fd)
        finally:
            os.close(fd)
        relative_path = fixture_dir.lstrip("/") + "/record"
        record_fd = open_beneath(upperfd, relative_path, os.O_RDONLY | O_NOATIME)
        try:
            require(os.fstat(record_fd).st_dev == rdev,
                    "fixture is not stored on the expected guest root device")
        finally:
            os.close(record_fd)
        read_upper_record(upperfd, relative_path)

        write_progress(progress_path, -1)
        writer_pid = os.fork()
        if writer_pid == 0:
            try:
                if upperfd is not None:
                    os.close(upperfd)
                if rootfd is not None:
                    os.close(rootfd)
                exit_code = writer_main(data_path, progress_path)
            except BaseException:
                traceback.print_exc()
                exit_code = 1
            os._exit(exit_code)
        wait_for_progress(progress_path, -1, 3.0)

        watchdog_status, status_child = socket.socketpair()
        watchdog_owner, owner_child = socket.socketpair()
        watchdog_pid = os.fork()
        if watchdog_pid == 0:
            try:
                watchdog_status.close()
                watchdog_owner.close()
                if upperfd is not None:
                    os.close(upperfd)
                exit_code = watchdog_main(rootfd, owner_child, status_child,
                                          result_path, WATCHDOG_SECONDS)
            except BaseException:
                traceback.print_exc()
                exit_code = 1
            os._exit(exit_code)
        freeze_attempted = True
        status_child.close()
        owner_child.close()

        ready_event = recv_event(watchdog_status, READY_SECONDS)
        require(ready_event is not None and ready_event.get("event") == "ready",
                f"watchdog did not confirm freeze: {ready_event!r}")

        controller_sock, worker_end = socket.socketpair()
        worker_pid = os.fork()
        if worker_pid == 0:
            try:
                controller_sock.close()
                watchdog_status.close()
                if upperfd is not None:
                    os.close(upperfd)
                if rootfd is not None:
                    os.close(rootfd)
                exit_code = worker_main(worker_end, watchdog_owner)
            except BaseException:
                traceback.print_exc()
                exit_code = 1
            os._exit(exit_code)
        worker_end.close()
        watchdog_owner.close()

        frozen_progress = read_progress(progress_path)
        first = read_upper_record(upperfd, relative_path)
        time.sleep(0.6)
        second = read_upper_record(upperfd, relative_path)
        require(first == second, "upper fixture changed during the freeze window")
        require(read_progress(progress_path) == frozen_progress,
                "writer progress advanced while the filesystem was frozen")

        if mode == "normal":
            controller_sock.sendall(b"R")
        elif mode == "disconnect":
            controller_sock.close()
            controller_sock = None
        elif mode == "death":
            os.kill(worker_pid, signal.SIGKILL)
        elif mode == "timeout":
            controller_sock.sendall(b"T")
        else:
            raise ProbeError(f"unsupported mode {mode!r}")

        final_event = recv_event(watchdog_status, WATCHDOG_SECONDS + 4.0)
        require(final_event is not None, "watchdog did not report thaw")
        watchdog_reason = final_event.get("reason")
        thaw_confirmed = final_event.get("thawed") is True
        require(thaw_confirmed, f"watchdog did not confirm thaw: {final_event!r}")

        expected_reason = {
            "normal": "release",
            "disconnect": "owner-gone",
            "death": "owner-gone",
            "timeout": "timeout",
        }[mode]
        require(watchdog_reason == expected_reason,
                f"expected watchdog reason {expected_reason!r}, got {watchdog_reason!r}")

        if mode == "timeout" and controller_sock is not None:
            controller_sock.sendall(b"X")
        if mode == "death":
            worker_status = wait_child(worker_pid, 1.0, statuses)
            require(os.waitstatus_to_exitcode(worker_status) == -signal.SIGKILL,
                    "SIGKILL mode did not kill the worker")
        else:
            worker_status = wait_child(worker_pid, WATCHDOG_SECONDS + 3.0, statuses)
            require(os.waitstatus_to_exitcode(worker_status) == 0,
                    f"worker failed: {os.waitstatus_to_exitcode(worker_status)}")
        worker_pid = None

        watchdog_status_code = wait_child(watchdog_pid, 2.0, statuses)
        require(os.waitstatus_to_exitcode(watchdog_status_code) == 0,
                "watchdog exited unsuccessfully")
        watchdog_pid = None

        after_progress = wait_for_progress(progress_path, frozen_progress, 5.0)
        writer_resumed = after_progress > frozen_progress
        capture_candidate = mode == "normal" and watchdog_reason == "release"
        scenario_passed = True

    except BaseException as exc:
        primary_error = exc
    finally:
        if controller_sock is not None and worker_pid is not None and not thaw_confirmed:
            try:
                controller_sock.sendall(b"C")
            except OSError:
                pass
        if controller_sock is not None:
            try:
                controller_sock.close()
            except OSError:
                pass

        if freeze_attempted and not thaw_confirmed and watchdog_status is not None:
            try:
                event = recv_event(watchdog_status, WATCHDOG_SECONDS + 2.0)
                if event is not None and event.get("thawed") is True:
                    thaw_confirmed = True
                    watchdog_reason = event.get("reason", watchdog_reason)
            except BaseException as exc:
                cleanup_errors.append(f"watchdog cleanup status failed: {exc}")
        if freeze_attempted and not thaw_confirmed and rootfd is not None:
            if emergency_thaw(rootfd, statuses, cleanup_errors):
                # Fallback unfreezes the guest filesystem, but never validates capture.
                thaw_confirmed = True
                capture_candidate = False

        if worker_pid is not None:
            stop_child(worker_pid, statuses, cleanup_errors)
        if writer_pid is not None:
            stop_child(writer_pid, statuses, cleanup_errors)
        if watchdog_pid is not None and thaw_confirmed:
            stop_child(watchdog_pid, statuses, cleanup_errors)

        if thaw_confirmed or not freeze_attempted:
            cleanup_fixture(fixture_dir, data_path, cleanup_errors)
        elif fixture_dir is not None:
            cleanup_errors.append("fixture left in place because thaw could not be confirmed")
        cleanup_scratch(scratch_dir, cleanup_errors)

        for sock in (controller_sock, worker_end, watchdog_status, watchdog_owner):
            if sock is not None:
                try:
                    sock.close()
                except OSError:
                    pass
        for fd in (upperfd, rootfd):
            if fd is not None:
                try:
                    os.close(fd)
                except OSError:
                    pass

    if primary_error is not None:
        print(f"probe failed: {type(primary_error).__name__}: {primary_error}", file=sys.stderr)
    if cleanup_errors:
        for error in cleanup_errors:
            print(f"cleanup failed: {error}", file=sys.stderr)
    if primary_error is not None or cleanup_errors or not scenario_passed or not writer_resumed:
        return 1

    print(json.dumps({
        "mode": mode,
        "scenarioPassed": True,
        "captureCandidate": capture_candidate,
        "watchdogReason": watchdog_reason,
        "writerResumed": writer_resumed,
    }, sort_keys=True))
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("normal", "disconnect", "death", "timeout"))
    args = parser.parse_args()
    return run_probe(args.mode)


if __name__ == "__main__":
    raise SystemExit(main())
