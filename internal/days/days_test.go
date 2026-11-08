package days

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTodayTashkentBoundary(t *testing.T) {
	// 18:59:59 UTC — ещё 23:59:59 того же дня в Ташкенте; 19:00:00 UTC — уже полночь следующего.
	if d := Today(time.Date(2026, 11, 8, 18, 59, 59, 0, time.UTC)); d != "2026-11-08" {
		t.Fatalf("23:59:59: %s", d)
	}
	if d := Today(time.Date(2026, 11, 8, 19, 0, 0, 0, time.UTC)); d != "2026-11-09" {
		t.Fatalf("00:00:00: %s", d)
	}
	if d := Today(time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC)); d != "2027-01-01" {
		t.Fatalf("новый год: %s", d)
	}
}

func TestNearAndValid(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	for day, want := range map[string]bool{"2026-11-07": true, "2026-11-08": true, "2026-11-09": true, "2026-11-10": false, "2026-10-08": false, "2026-02-30": false, "x": false} {
		if Near(day, now) != want {
			t.Errorf("Near(%s) != %v", day, want)
		}
	}
}

func TestRel(t *testing.T) {
	p := Project{Slug: "hub", Folder: "Хаб", Ext: "l7copy"}
	r, err := Rel(p, "2026-11-08")
	if err != nil || r != filepath.Join("Хаб", "Копии базы", "2026-11", "hub-2026-11-08.l7copy") {
		t.Fatalf("%q %v", r, err)
	}
	for _, bad := range []Project{{Slug: "../x", Folder: "Хаб", Ext: "x"}, {Slug: "hub", Folder: "../Хаб", Ext: "x"}, {Slug: "hub", Folder: "", Ext: "x"}} {
		if _, err := Rel(bad, "2026-11-08"); err == nil {
			t.Errorf("должна быть ошибка: %+v", bad)
		}
	}
	if _, err := Rel(p, "2026-13-01"); err == nil {
		t.Error("неверная дата")
	}
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestPutReplacesAndKeepsWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Хаб", "Копии базы", "2026-11", "hub-2026-11-08.l7copy")
	if Exists(path) {
		t.Fatal("файла ещё нет")
	}
	if err := Put(path, []byte("первая"), sha([]byte("первая"))); err != nil {
		t.Fatal(err)
	}
	if err := Put(path, []byte("первая"), ""); !errors.Is(err, ErrSame) {
		t.Fatalf("тот же файл: %v", err)
	}
	if err := Put(path, []byte("вторая"), sha([]byte("вторая"))); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "вторая" || !Exists(path) {
		t.Fatalf("в файле %q", b)
	}
	// Дошла не целой — файл дня остаётся прежним, временного нет.
	if err := Put(path, []byte("испорчена"), sha([]byte("другое"))); err == nil {
		t.Fatal("ждали ошибку")
	}
	b, _ = os.ReadFile(path)
	if string(b) != "вторая" {
		t.Fatalf("файл дня испорчен: %q", b)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("временный файл остался")
	}
}
