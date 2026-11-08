//go:build windows

package autostart

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const valueName = "Little Hub"

// InstallPath — %LOCALAPPDATA%\Programs\Little Hub\LittleHub.exe (без прав администратора).
func InstallPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = d
	}
	return filepath.Join(base, "Programs", "Little Hub", "LittleHub.exe"), nil
}

// Enable — запуск при входе в Windows.
func Enable(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, `"`+exe+`"`)
}

// Disable — убрать из автозапуска.
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

// Start — запустить установленную программу отдельно от этой (окно консоли не появляется).
func Start(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Restart — после обновления: запустить новую версию; эта сразу выходит (вызывающий делает os.Exit).
func Restart(exe string) error { return Start(exe, "--updated") }
