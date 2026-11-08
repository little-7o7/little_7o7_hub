//go:build windows

package secret

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type dpapi struct{ path string }

// New — ключ в файле token.bin, зашифрованный DPAPI текущего пользователя.
func New(dir string) Store { return dpapi{filepath.Join(dir, "token.bin")} }

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func take(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

var entropy = []byte("little7o7-hub-token")

func (d dpapi) Get() (string, error) {
	b, err := os.ReadFile(d.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNone
	}
	if err != nil {
		return "", err
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	return string(take(&out)), nil
}

func (d dpapi) Set(token string) error {
	var out windows.DataBlob
	name, _ := windows.UTF16PtrFromString("Little Hub")
	if err := windows.CryptProtectData(blob([]byte(token)), name, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, take(&out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

func (d dpapi) Delete() error {
	if err := os.Remove(d.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
