//go:build darwin

package tray

import (
	_ "embed"

	"fyne.io/systray"
)

//go:embed icon.png
var iconPNG []byte

//go:embed template.png
var templatePNG []byte

// В строке меню Mac — чёрно-белый значок (macOS сам красит его под светлую и тёмную тему).
func setIcon() { systray.SetTemplateIcon(templatePNG, iconPNG) }
