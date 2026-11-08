//go:build !windows

package files

// На Mac и Linux файл с точкой в начале имени и так скрыт.
func hide(string)   {}
func unhide(string) {}
