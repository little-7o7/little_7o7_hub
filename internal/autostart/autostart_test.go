package autostart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopySelfAndReplace(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "Little Hub", "LittleHub")
	if err := CopySelf(dst, -1); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() == 0 {
		t.Fatal(err)
	}
	// Второй раз — старый файл откладывается в .old; size — только начало файла (без хвоста установщика).
	if err := CopySelf(dst, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + ".old"); err != nil {
		t.Fatal(".old нет")
	}
	if st, err := os.Stat(dst); err != nil || st.Size() != 100 {
		t.Fatal("скопировано не 100 байт", err)
	}
	if p, err := InstallPath(); err != nil || p == "" {
		t.Fatal(p, err)
	}
}
