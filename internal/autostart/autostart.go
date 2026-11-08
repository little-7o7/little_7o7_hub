// Package autostart — где стоит программа и как она запускается при входе в систему (план pc-backup-1, решение 3):
// Windows — ключ HKCU\…\Run (без прав администратора), Mac — LaunchAgent пользователя (перезапуск после сбоя).
package autostart

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// CopySelf — поставить этот файл программы на её место (dst). Уже там — ничего не делать.
func CopySelf(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if a, err1 := filepath.EvalSymlinks(src); err1 == nil {
		src = a
	}
	if b, err1 := filepath.EvalSymlinks(dst); err1 == nil && b == src {
		return nil
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := errors.Join(cerr, out.Close()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Программа уже работает с этого места (переустановка) — старый файл откладываем: на Windows работающий файл
	// нельзя заменить, но можно переименовать.
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, dst)
}
