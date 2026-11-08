package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/secret"
)

// Рядом с копиями: журнал, настройки, «Как восстановить.txt», проверка копий раз в неделю (пункты 82–84, 86).

func TestKeepProgramAndRestore(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`, projects: `{"projects":[]}`}
	a, disk := newApp(t, f, &now)
	_ = a.SetLang("en")
	ctx := context.Background()
	a.tick(ctx)
	if err := a.SyncFiles(ctx); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(disk, "Little Hub")
	txt, err := os.ReadFile(filepath.Join(root, RestoreFile))
	if err != nil || !strings.Contains(string(txt), "ВЕРНУТЬ БАЗУ") || !strings.HasPrefix(string(txt), "\xef\xbb\xbf") {
		t.Fatal("Как восстановить.txt", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ProgramFolder, "Журнал.txt")); !strings.Contains(string(b), "Подключён к хабу") {
		t.Fatal("журнал")
	}
	b, err := os.ReadFile(filepath.Join(root, ProgramFolder, SettingsFile))
	var s Saved
	if err != nil || json.Unmarshal(b, &s) != nil || s.Lang != "en" || len(s.Disks) != 1 || s.Disks[0] != disk || strings.Contains(string(b), "Bearer") {
		t.Fatalf("настройки: %s", b)
	}
	// Те же настройки — файл не переписываем (время то же).
	now = now.Add(time.Hour)
	a.keepProgram(root)
	if b2, _ := os.ReadFile(filepath.Join(root, ProgramFolder, SettingsFile)); string(b2) != string(b) {
		t.Fatal("переписан без изменений")
	}

	// Переустановка: новая программа без дисков находит настройки на диске и берёт диски и язык.
	dir := t.TempDir()
	cfg, _ := config.Load(dir)
	b2 := &App{Version: "1.3.0", Dir: dir, Cfg: cfg, Secret: secret.New(dir), Now: func() time.Time { return now },
		Space:      func(string) (disks.Space, error) { return disks.Space{Free: 500e9, Total: 1e12}, nil },
		Candidates: func() []string { return []string{t.TempDir(), disk} }}
	if _, err := b2.Pair(ctx, f.server(t).URL, "abcd-1234-abcd-1234"); err != nil {
		t.Fatal(err)
	}
	if c := b2.Cfg.Get(); len(c.Disks) != 1 || c.Disks[0] != disk || c.Lang != "en" || !hasLog(b2, "прежней установки") {
		t.Fatalf("восстановление: %+v", c.Disks)
	}
	if _, _, ok := findSaved([]string{t.TempDir()}); ok {
		t.Fatal("нашлось на пустом диске")
	}
}

func TestVerifyCopies(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`, projects: `{"projects":[]}`}
	a, disk := newApp(t, f, &now)
	root := filepath.Join(disk, "Little Hub")
	ctx := context.Background()
	a.tick(ctx) // копия за сегодня — записана с SHA-256
	// Копия от прежней программы (без записи) и испорченная после записи.
	old, _ := days.Rel(HubProject, "2026-11-06")
	_ = os.MkdirAll(filepath.Dir(filepath.Join(root, old)), 0o755)
	_ = os.WriteFile(filepath.Join(root, old), sample(t), 0o644)
	today, _ := days.Rel(HubProject, "2026-11-08")
	data, _ := os.ReadFile(filepath.Join(root, today))
	data[len(data)-1] ^= 1
	_ = os.WriteFile(filepath.Join(root, today), data, 0o644)
	a.verify(root, nil)
	if days.Exists(filepath.Join(root, today)) || !days.Exists(filepath.Join(root, old)) || !hasLog(a, "повреждены") {
		t.Fatal("проверка")
	}
	// Неделя не прошла — не проверяем; прошла — копия от прежней программы уже запомнена и цела.
	b, _ := os.ReadFile(filepath.Join(root, "Хаб", "Копии базы", days.ShaFile))
	if !strings.Contains(string(b), "2026-11/hub-2026-11-06.l7copy") {
		t.Fatal("не запомнена", string(b))
	}
	_ = os.WriteFile(filepath.Join(root, old), []byte("junk"), 0o644)
	a.verify(root, nil)
	if !days.Exists(filepath.Join(root, old)) {
		t.Fatal("проверили раньше недели")
	}
	now = now.Add(CheckEvery)
	a.verify(root, nil)
	if days.Exists(filepath.Join(root, old)) {
		t.Fatal("испорченная осталась")
	}
}
