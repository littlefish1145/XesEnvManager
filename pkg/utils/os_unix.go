//go:build !windows
// +build !windows

package utils

import (
	"os/exec"
	"syscall"
)

// GetAppDataPath returns empty string on non-Windows systems
func GetAppDataPath() string {
	return ""
}

// TerminateProcessTree terminates the process tree on Unix-like systems
func TerminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Send SIGTERM to the process group
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}

	// Fallback to killing the main process
	return cmd.Process.Kill()
}