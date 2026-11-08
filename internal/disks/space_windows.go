//go:build windows

package disks

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Free — место на диске по пути (GetDiskFreeSpaceEx: свободно именно этому пользователю).
func Free(path string) (Space, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Space{}, err
	}
	var free, total, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &all); err != nil {
		return Space{}, err
	}
	return Space{Free: free, Total: total}, nil
}

func fixedDrives() []string {
	var out []string
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if t := windows.GetDriveType(p); t == windows.DRIVE_FIXED || t == windows.DRIVE_REMOVABLE {
			out = append(out, root)
		}
	}
	return out
}

// Suggest — диски компьютера (встроенные и внешние).
func Suggest() []string { return fixedDrives() }

// Default — диск по умолчанию: первый не системный (D:\), а нет его — «Документы».
func Default() string {
	sys := filepath.VolumeName(os.Getenv("SystemRoot")) + `\`
	for _, d := range fixedDrives() {
		p, _ := windows.UTF16PtrFromString(d)
		if d != sys && windows.GetDriveType(p) == windows.DRIVE_FIXED {
			return d
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return sys
	}
	return filepath.Join(home, "Documents")
}
