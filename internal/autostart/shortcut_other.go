//go:build !windows

package autostart

// Ярлыки — только на Windows: на Mac программа в строке меню и в автозапуске (LaunchAgent).
func Shortcuts(string, []byte) error { return nil }
func RemoveShortcuts() error         { return nil }
