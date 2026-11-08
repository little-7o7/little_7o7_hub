//go:build !windows && !darwin

package secret

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.Get(); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
	if err := s.Set("ключ"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(); err != nil || v != "ключ" {
		t.Fatal(v, err)
	}
	st, _ := os.Stat(filepath.Join(dir, "token"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("права %v", st.Mode().Perm())
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
}
