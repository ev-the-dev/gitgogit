package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gitgogit/config"
)

// WritePID atomically writes the current process PID to path. It creates parent directories as needed, writes to a temp file in the same directory, then renames it into place so readers never see a partial write.
func WritePID(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), config.DirPerm); err != nil {
		return fmt.Errorf("create pid dir: %w", err)
	}
	tmp := path + ".tmp"
	content := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(tmp, []byte(content), config.FilePerm); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename pid: %w", err)
	}
	return nil
}

// ReadPID reads and returns the PID stored in path.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid in %q: %w", path, err)
	}
	return pid, nil
}

// RemovePID deletes the PID file. Returns nil if the file does not exist.
func RemovePID(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// IsRunning reads the PID file and checks whether the process is alive. Returns running=false (no error) if the file does not exist. Uses signal 0 to test liveness without sending a real signal.
func IsRunning(path string) (pid int, running bool, err error) {
	pid, err = ReadPID(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	err = syscall.Kill(pid, 0)
	if err == nil {
		return pid, true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return pid, false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		// Process exists but we lack permission to signal it — treat as running.
		return pid, true, nil
	}
	return pid, false, fmt.Errorf("signal pid %d: %w", pid, err)
}
