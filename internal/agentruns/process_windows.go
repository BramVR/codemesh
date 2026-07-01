//go:build windows

package agentruns

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

const (
	lockFileFailImmediately = 0x00000001
	lockFileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func configureCommandProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func killCommandProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	if err == nil {
		return nil
	}
	return command.Process.Kill()
}

func acquireRunLock(runDir string) (func(), error) {
	lockPath, err := runLockPath(runDir)
	if err != nil {
		return nil, err
	}
	file, err := openLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("open agent run lock: %w", err)
	}
	var overlapped syscall.Overlapped
	err = lockFileEx(
		file,
		lockFileExclusiveLock|lockFileFailImmediately,
		&overlapped,
	)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, errorLockViolation) {
			return nil, fmt.Errorf("%w: %s", errRunLocked, filepath.Base(runDir))
		}
		return nil, fmt.Errorf("lock agent run: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockFileEx(file, &overlapped)
		_ = file.Close()
		return nil, fmt.Errorf("truncate agent run lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = unlockFileEx(file, &overlapped)
		_ = file.Close()
		return nil, fmt.Errorf("rewind agent run lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d\ncreated_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = unlockFileEx(file, &overlapped)
		_ = file.Close()
		return nil, fmt.Errorf("write agent run lock: %w", err)
	}
	return func() {
		_ = unlockFileEx(file, &overlapped)
		_ = file.Close()
	}, nil
}

func lockFileEx(file *os.File, flags uint32, overlapped *syscall.Overlapped) error {
	r1, _, err := procLockFileEx.Call(
		file.Fd(),
		uintptr(flags),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if r1 == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}

func unlockFileEx(file *os.File, overlapped *syscall.Overlapped) error {
	r1, _, err := procUnlockFileEx.Call(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if r1 == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}
