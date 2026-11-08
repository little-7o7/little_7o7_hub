package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
)

// Окно «Файлы»: файлы под паролем копий (пункты 85 и 109).

const copyPass = "образец-пароля-копий" // им сделаны образцы копий (copyfmt/testdata)

func lockJSON(t *testing.T) (string, fileseal.Lock) {
	t.Helper()
	salt, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	l, err := fileseal.LockOf(copyPass, salt, 1000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(l)
	return string(b), l
}

func TestVault(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	lj, lock := lockJSON(t)
	f := &fakeHub{
		copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`,
		projects: `{"lock":` + lj + `,"projects":[{"slug":"sarpo","folder":"Sarpo Business","copy":false,"ok":true,"businesses":[{"id":7,"slug":"sarpotextile","name":"Sarpo Textile","kind":"shop","full":true,"ver":"v1","files":[{"key":"state","folder":"Excel","name":"Магазин.xlsx","refresh":false}]}]}]}`,
		answers:  map[string]string{"sarpo/excel?b=7&f=state": "excel-данные", "sarpo/items?arch=1&b=7": `{"groups":[]}`},
	}
	a, disk := newApp(t, f, &now)
	a.OpenDir = filepath.Join(t.TempDir(), "open")
	ctx := context.Background()
	a.tick(ctx)
	if err := a.SyncFiles(ctx); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(disk, "Little Hub")
	excel := filepath.Join(root, "Sarpo Business", "sarpotextile", "Excel", "Магазин.xlsx")
	if _, err := os.Stat(excel); err == nil {
		t.Fatal("Excel лежит открытым")
	}
	if c := a.Cfg.Get().Lock; c == nil || c.Tag() != lock.Tag() || !a.View().Sealed {
		t.Fatal("ключ не запомнен")
	}
	// Список: имена без «.l7», служебных нет.
	fl, err := a.Browse("Sarpo Business/sarpotextile/Excel")
	if err != nil || len(fl.Entries) != 1 || fl.Entries[0].Name != "Магазин.xlsx" || !fl.Entries[0].Sealed || fl.Unlocked || !fl.Sealed {
		t.Fatalf("список: %+v %v", fl, err)
	}
	biz, _ := a.Browse("Sarpo Business/sarpotextile")
	for _, e := range biz.Entries {
		if strings.HasPrefix(e.Name, ".") {
			t.Fatal("служебный файл в окне", e.Name)
		}
	}
	for _, bad := range []string{"..", "../..", "Sarpo Business/../../x", `Sarpo Business\x`, "C:/Windows", "Sarpo Business/sarpotextile/.littlehub-ver"} {
		if _, err := a.Browse(bad); err == nil {
			t.Fatal("путь принят", bad)
		}
		if _, err := a.OpenFile(bad); err == nil {
			t.Fatal("открыт", bad)
		}
	}
	// Без пароля — просим пароль; чужой — не подходит.
	if _, err := a.OpenFile("Sarpo Business/sarpotextile/Excel/Магазин.xlsx"); !errors.Is(err, ErrNeedPassword) {
		t.Fatal("без пароля", err)
	}
	if err := a.Unlock("чужой пароль"); !errors.Is(err, fileseal.ErrPassword) {
		t.Fatal("чужой пароль", err)
	}
	if err := a.Unlock(""); !errors.Is(err, ErrNeedPassword) {
		t.Fatal("пустой", err)
	}
	if err := a.Unlock(copyPass); err != nil || !a.View().Unlocked {
		t.Fatal(err)
	}
	o, err := a.OpenFile("Sarpo Business/sarpotextile/Excel/Магазин.xlsx")
	if b, _ := os.ReadFile(o.Path); err != nil || string(b) != "excel-данные" || o.Reveal || !strings.HasPrefix(o.Path, a.OpenDir) {
		t.Fatalf("открытие: %+v %v", o, err)
	}
	// Копия базы хаба — SQL рядом, показываем папку.
	copyRel := "Хаб/Копии базы/2026-11/hub-2026-11-08.l7copy"
	o2, err := a.OpenFile(copyRel)
	if b, _ := os.ReadFile(o2.Path); err != nil || !o2.Reveal || !strings.HasSuffix(o2.Path, ".sql") || len(b) == 0 {
		t.Fatalf("копия: %+v %v", o2, err)
	}
	// Папка — как есть.
	if o3, err := a.OpenFile("Sarpo Business"); err != nil || o3.Path != filepath.Join(root, "Sarpo Business") {
		t.Fatal(o3, err)
	}
	if _, err := a.OpenFile("Sarpo Business/нет.xlsx"); err == nil {
		t.Fatal("нет файла")
	}
	// «Сохранить открытыми» — папка бизнеса целиком, рядом с «Little Hub».
	dest, n, err := a.Export("Sarpo Business/sarpotextile")
	if b, _ := os.ReadFile(filepath.Join(dest, "Sarpo Business", "sarpotextile", "Excel", "Магазин.xlsx")); err != nil || n != 1 || string(b) != "excel-данные" || filepath.Dir(filepath.Dir(dest)) != disk {
		t.Fatalf("экспорт: %s %d %v", dest, n, err)
	}
	if _, n, err := a.Export(copyRel); err != nil || n != 1 {
		t.Fatal("экспорт копии", n, err)
	}
	// 15 минут — пароль забыт; «Закрыть пароль» — сразу.
	now = now.Add(PassKeep + time.Second)
	if _, err := a.OpenFile("Sarpo Business/sarpotextile/Excel/Магазин.xlsx"); !errors.Is(err, ErrNeedPassword) {
		t.Fatal("пароль не забыт", err)
	}
	if _, _, err := a.Export(""); !errors.Is(err, ErrNeedPassword) {
		t.Fatal("экспорт без пароля", err)
	}
	_ = a.Unlock(copyPass)
	a.Forget()
	if a.keysNow() != nil {
		t.Fatal("Forget")
	}
	// Временные: старше часа — стереть; при выходе — все.
	list, _ := os.ReadDir(a.OpenDir)
	if len(list) != 2 {
		t.Fatal("временные", len(list))
	}
	old := now.Add(-2 * time.Hour)
	_ = os.Chtimes(filepath.Join(a.OpenDir, list[0].Name()), old, old)
	_ = os.Chtimes(filepath.Join(a.OpenDir, list[1].Name()), now, now)
	a.CleanOpen(false)
	if l, _ := os.ReadDir(a.OpenDir); len(l) != 1 {
		t.Fatal("уборка через час", len(l))
	}
	a.CleanOpen(true)
	if l, _ := os.ReadDir(a.OpenDir); len(l) != 0 {
		t.Fatal("уборка при выходе", len(l))
	}
}

func TestVaultPlain(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{
		copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`,
		projects: `{"projects":[{"slug":"sarpo","folder":"Sarpo Business","copy":false,"ok":true,"businesses":[{"id":7,"slug":"s","name":"S","kind":"shop","full":true,"ver":"v1","files":[{"key":"state","folder":"","name":"A.xlsx","refresh":false}]}]}]}`,
		answers:  map[string]string{"sarpo/excel?b=7&f=state": "a", "sarpo/items?arch=1&b=7": `{"groups":[]}`},
	}
	a, disk := newApp(t, f, &now)
	a.OpenDir = t.TempDir()
	if err := a.SyncFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Пароль копий не задан: файл открытый — открывается прямо с диска, пароль не нужен.
	o, err := a.OpenFile("Sarpo Business/s/A.xlsx")
	if err != nil || o.Path != filepath.Join(disk, "Little Hub", "Sarpo Business", "s", "A.xlsx") {
		t.Fatal(o, err)
	}
	if err := a.Unlock("любой"); err != nil {
		t.Fatal(err)
	}
	if _, n, err := a.Export("Sarpo Business"); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if fl, _ := a.Browse(""); fl.Sealed {
		t.Fatal("ключа нет")
	}
	// Установщик из «Программ» окно только показывает в папке, не запускает.
	exe := filepath.Join(disk, "Little Hub", "Программы", "Little Hub", "LittleHub-1.2.1-windows.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o, err := a.OpenFile("Программы/Little Hub/LittleHub-1.2.1-windows.exe"); err != nil || !o.Reveal || o.Path != exe {
		t.Fatal("установщик открыт, а не показан:", o, err)
	}
}
