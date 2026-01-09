//go:build windows
// +build windows

package utils

import (
	"os"
	"os/exec"
	"golang.org/x/sys/windows/registry"
)

// TerminateProcessTree terminates the process tree on Windows
func TerminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// For now, just kill the main process
	// A full implementation would require Windows API calls
	// to enumerate and kill child processes
	_ = cmd.Process.Kill()
	return nil
}

// GetAppDataPath gets Windows AppData path
func GetAppDataPath() string {
	// For Windows, try to get AppData path
	appData := os.Getenv("APPDATA")
	if appData != "" {
		return appData
	}
	
	// Alternative: try to read from registry
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\Shell Folders`, registry.READ)
	if err == nil {
		defer key.Close()
		
		appDataPath, _, err := key.GetStringValue("AppData")
		if err == nil {
			return appDataPath
		}
	}
	
	return ""
}