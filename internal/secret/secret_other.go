//go:build !windows && !darwin

package secret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type file struct{ path string }

// New — на Linux (тесты, разработка): файл с правами только для владельца.
func New(dir string) Store { return file{filepath.Join(dir, "token")} }

func (f file) Get() (string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNone
	}
	return strings.TrimSpace(string(b)), err
}

func (f file) Set(token string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.path, []byte(token), 0o600)
}

func (f file) Delete() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
