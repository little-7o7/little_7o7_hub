//go:build windows

package autostart

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Ярлыки «Little Hub» в меню «Пуск» и на рабочем столе (владелец 2026-11-08: «kak otkrit»): после «Выход» программу
// открывают ярлыком; вторая копия откроет окно первой. Ярлыки делает PowerShell через WScript.Shell, пути — через
// переменные окружения (без подстановки в текст команды).
const shortcutPS = `$ErrorActionPreference='Stop'; $w=New-Object -ComObject WScript.Shell; ` +
	`foreach($d in @([Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('DesktopDirectory'))){ if($d){ ` +
	`$s=$w.CreateShortcut((Join-Path $d 'Little Hub.lnk')); $s.TargetPath=$env:LH_EXE; $s.WorkingDirectory=(Split-Path -Parent $env:LH_EXE); ` +
	`if($env:LH_ICO){ $s.IconLocation=$env:LH_ICO+',0' }; $s.Description='Little Hub'; $s.Save() } }`

const removePS = `foreach($d in @([Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('DesktopDirectory'))){ if($d){ ` +
	`Remove-Item -LiteralPath (Join-Path $d 'Little Hub.lnk') -ErrorAction SilentlyContinue } }`

func powershell(script string, env ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd.Run()
}

// Shortcuts — ярлыки на exe; ico — значок (кладётся рядом с программой как LittleHub.ico).
func Shortcuts(exe string, ico []byte) error {
	icoPath := ""
	if len(ico) > 0 {
		p := filepath.Join(filepath.Dir(exe), "LittleHub.ico")
		if err := os.WriteFile(p, ico, 0o644); err == nil {
			icoPath = p
		}
	}
	return powershell(shortcutPS, "LH_EXE="+exe, "LH_ICO="+icoPath)
}

// RemoveShortcuts — убрать ярлыки (--uninstall).
func RemoveShortcuts() error { return powershell(removePS) }
