//go:build windows

package tray

import (
	_ "embed"

	"fyne.io/systray"
)

//go:embed icon.ico
var iconICO []byte

func setIcon() { systray.SetIcon(iconICO) }
