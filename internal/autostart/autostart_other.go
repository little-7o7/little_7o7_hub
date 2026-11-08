//go:build !windows && !darwin

package autostart

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// На Linux программа только для разработки: ставится в ~/.local/bin, без автозапуска.
func InstallPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "littlehub"), nil
}

func Enable(string) error { return errors.New("автозапуск — только Windows и Mac") }
func Disable() error      { return nil }

func Start(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func Restart(exe string) error { return Start(exe, "--updated") }
