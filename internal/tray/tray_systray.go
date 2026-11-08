//go:build windows || darwin

package tray

import (
	"runtime"
	"time"

	"fyne.io/systray"
)

// Run — значок и меню; возвращается после «Выход» (или Stop). ready — значок появился.
func Run(m Menu, ready func()) {
	systray.Run(func() {
		setIcon()
		w := label(m)
		systray.SetTooltip(w[3])
		open := systray.AddMenuItem(w[0], "")
		save := systray.AddMenuItem(w[1], "")
		systray.AddSeparator()
		quit := systray.AddMenuItem(w[2], "")
		if runtime.GOOS == "windows" {
			// На Windows щелчок по значку открывает окно, меню — правой кнопкой.
			systray.SetOnTapped(m.Open)
		}
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					m.Open()
				case <-save.ClickedCh:
					m.Save()
				case <-quit.ClickedCh:
					m.Quit()
					return
				}
			}
		}()
		go func() {
			for {
				w := label(m)
				open.SetTitle(w[0])
				save.SetTitle(w[1])
				quit.SetTitle(w[2])
				if m.Tooltip != nil {
					systray.SetTooltip(m.Tooltip())
				}
				time.Sleep(time.Minute)
			}
		}()
		if ready != nil {
			ready()
		}
	}, nil)
}

// Stop — убрать значок (Run вернётся).
func Stop() { systray.Quit() }
