//go:build windows

package files

import "golang.org/x/sys/windows"

// hide — служебный файл скрытым (в Проводнике не мешает).
func hide(path string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	if a, err := windows.GetFileAttributes(p); err == nil {
		_ = windows.SetFileAttributes(p, a|windows.FILE_ATTRIBUTE_HIDDEN)
	}
}

// unhide — снять «скрытый» перед заменой: Windows не даёт перезаписать скрытый файл обычным способом.
func unhide(path string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	if a, err := windows.GetFileAttributes(p); err == nil && a&windows.FILE_ATTRIBUTE_HIDDEN != 0 {
		_ = windows.SetFileAttributes(p, a&^windows.FILE_ATTRIBUTE_HIDDEN)
	}
}
