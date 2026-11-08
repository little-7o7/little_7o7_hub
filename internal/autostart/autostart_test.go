package autostart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopySelfAndReplace(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "Little Hub", "LittleHub")
	if err := CopySelf(dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() == 0 {
		t.Fatal(err)
	}
	// Второй раз — старый файл откладывается в .old.
	if err := CopySelf(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + ".old"); err != nil {
		t.Fatal(".old нет")
	}
	if p, err := InstallPath(); err != nil || p == "" {
		t.Fatal(p, err)
	}
}
