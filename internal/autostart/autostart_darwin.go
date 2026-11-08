//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const label = "uz.little7o7.hub"

// InstallPath — ~/Library/Application Support/Little Hub/LittleHub.
func InstallPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Little Hub", "LittleHub"), nil
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func xml(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Plist — LaunchAgent: запуск при входе, перезапуск, если программа упала (выход «Выход» из меню — код 0, без перезапуска).
func Plist(exe, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, xml(exe), xml(logPath), xml(logPath))
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// Enable — LaunchAgent и сразу запуск (launchctl bootstrap запускает его, RunAtLoad).
func Enable(exe string) error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, "Library", "Logs", "Little Hub.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(Plist(exe, logPath)), 0o644); err != nil {
		return err
	}
	_ = exec.Command("/bin/launchctl", "bootout", domain()+"/"+label).Run()
	if out, err := exec.Command("/bin/launchctl", "bootstrap", domain(), p).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl: %v: %s", err, out)
	}
	return nil
}

// Disable — убрать LaunchAgent.
func Disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	_ = exec.Command("/bin/launchctl", "bootout", domain()+"/"+label).Run()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Start — на Mac программу запускает launchd (Enable); вручную — отдельным процессом.
func Start(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Restart — после обновления: launchd перезапускает службу уже с новым файлом. Не под launchd — новый процесс.
func Restart(exe string) error {
	if p, err := plistPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			return exec.Command("/bin/launchctl", "kickstart", "-k", domain()+"/"+label).Start()
		}
	}
	return Start(exe, "--updated")
}
