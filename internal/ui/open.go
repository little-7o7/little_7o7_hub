package ui

import (
	"os/exec"
	"runtime"
)

// OpenBrowser — открыть ссылку в браузере по умолчанию.
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("/usr/bin/open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
