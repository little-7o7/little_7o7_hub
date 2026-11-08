package files

import (
	"context"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Файлы под паролем копий и «Старые версии» (пункты 108, 109).

const pass = "пароль-копий-2026"

func testLock(t *testing.T) *fileseal.Lock {
	t.Helper()
	salt, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	l, err := fileseal.LockOf(pass, salt, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return &l
}

func opened(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	p, err := fileseal.Open(b, &fileseal.Keys{Password: pass})
	if err != nil {
		t.Fatal(path, err)
	}
	return string(p)
}

func TestSealedBusiness(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "Sarpo Business", "shop")
	f := &fake{limit: 25}
	f.items = func(q url.Values) (any, error) {
		if q.Get("kind") == "" {
			return map[string]any{"groups": []Group{{Kind: "product", Folder: "Товары", Archive: "Товары (архив)", Files: []string{"О товаре.xlsx"}, Items: []Item{{ID: 1, Dir: "Ткань", Ver: "1", Photos: []Photo{{Key: "p", File: "Фото", Ver: "1"}}}}}}}, nil
		}
		return map[string]any{"items": []map[string]any{{"id": 1, "files": []map[string]any{{"name": "О товаре.xlsx", "data": []byte("товар")}}}}, "more": []int{}}, nil
	}
	b := hub.Business{ID: 7, Slug: "shop", Name: "Shop", Ver: "v1", Files: []hub.PcFile{{Key: "state", Folder: "Excel", Name: "Shop.xlsx"}, {Key: "day:2026-11-07", Folder: "Продажи", Name: "Продажи-2026-11-07.xlsx"}}}
	// Программа 1.2 разложила открытыми.
	if err := Business(ctx, f, b, dir, &Disk{Root: root, Today: "2026-11-07"}, false, &Result{}, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "Excel", "Shop.xlsx") == "" || read(t, dir, "Товары", "Ткань", "Фото.jpg") == "" {
		t.Fatal("открытые файлы")
	}
	old := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	_ = os.Chtimes(filepath.Join(dir, "Продажи", "Продажи-2026-11-07.xlsx"), old, old)
	_ = os.Chtimes(filepath.Join(dir, "Excel", "Shop.xlsx"), old, old)
	// Пароль копий задан: та же версия бизнеса, но проходим заново — всё шифруется, открытых не остаётся.
	d := &Disk{Root: root, Today: "2026-11-08", Lock: testLock(t)}
	excel := f.excel
	if err := Business(ctx, f, b, dir, d, false, &Result{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Excel/Shop.xlsx", "Продажи/Продажи-2026-11-07.xlsx", "Товары/Ткань/О товаре.xlsx", "Товары/Ткань/Фото.jpg"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if exists(full) || opened(t, full+fileseal.Ext) == "" {
			t.Fatal("не зашифрован", p)
		}
	}
	// Прошедший день не скачивали заново — зашифровали на месте, время записи то же.
	if st, _ := os.Stat(filepath.Join(dir, "Продажи", "Продажи-2026-11-07.xlsx.l7")); !st.ModTime().Equal(old) {
		t.Fatal("время записи", st.ModTime())
	}
	// «Что есть сейчас» скачан заново (бизнес «изменился» — новый ключ), прежний — вчерашний — в «Старых версиях».
	if f.excel != excel+1 || opened(t, filepath.Join(dir, "Excel", "Shop.xlsx.l7")) != "excel?b=7&f=state" {
		t.Fatal("Excel", f.excel-excel)
	}
	kept := filepath.Join(root, OldFolder, "2026-11-07", "Sarpo Business", "shop", "Excel", "Shop.xlsx.l7")
	if opened(t, kept) != "excel?b=7&f=state" {
		t.Fatal("старая версия")
	}
	if ReadVer(dir) != "v1 l7:"+d.Lock.Tag() {
		t.Fatal(ReadVer(dir))
	}
	// В тот же день заменяем без «Старых версий».
	if err := Excel(ctx, f, b, dir, d, true, &Result{}); err != nil {
		t.Fatal(err)
	}
	if es := names(t, filepath.Join(root, OldFolder)); len(es) != 1 {
		t.Fatal("вторая старая версия за день", es)
	}
	// Та же версия и ключ — проект не спрашиваем.
	asked := f.asked
	if err := Business(ctx, f, b, dir, d, false, &Result{}, nil); err != nil || f.asked != asked {
		t.Fatal("спросили снова")
	}
	// Фото пропало у проекта под другим именем — переименовываем зашифрованное.
	k := loadKept(filepath.Join(dir, "Товары", Manifest))["1"]
	if k.Photos["p"] != "1|Фото.jpg" {
		t.Fatal(k.Photos)
	}
}

func TestDiskHelpers(t *testing.T) {
	root := t.TempDir()
	d := &Disk{Root: root, Today: "2026-11-08", Lock: testLock(t)}
	p := filepath.Join(root, "a.xlsx")
	if ok, err := d.write(p, []byte("one")); !ok || err != nil || opened(t, p+".l7") != "one" {
		t.Fatal(err)
	}
	// Остался и открытый (был открыт в Excel при шифровании) — has убирает его.
	_ = os.WriteFile(p, []byte("plain"), 0o644)
	if !d.has(p) || exists(p) {
		t.Fatal("открытый остался")
	}
	if err := rename(p, filepath.Join(root, "b.xlsx")); err != nil || find(filepath.Join(root, "b.xlsx")) != filepath.Join(root, "b.xlsx.l7") {
		t.Fatal("переименование", err)
	}
	if rename(p, filepath.Join(root, "c.xlsx")) == nil {
		t.Fatal("нет файла — ошибка")
	}
	remove(filepath.Join(root, "b.xlsx"))
	if find(filepath.Join(root, "b.xlsx")) != "" || d.has(filepath.Join(root, "b.xlsx")) {
		t.Fatal("не удалён")
	}
	// Без ключа — открытый; зашифрованный того же имени убирается.
	plain := &Disk{Root: root, Today: "2026-11-08"}
	_, _ = d.write(p, []byte("x"))
	if ok, _ := plain.write(p, []byte("open")); !ok || read(t, p) != "open" || exists(p+".l7") || plain.tag() != "" {
		t.Fatal("открытый")
	}
	// «Старые версии» старше 30 дней — удалить; не дни — не трогать.
	for _, day := range []string{"2026-09-01", "2026-11-01", "заметки"} {
		_ = os.MkdirAll(filepath.Join(root, OldFolder, day), 0o755)
	}
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	if n := PruneOld(root, now, 30); n != 1 || !has(names(t, filepath.Join(root, OldFolder)), "2026-11-01") || !has(names(t, filepath.Join(root, OldFolder)), "заметки") {
		t.Fatal("уборка", n)
	}
	_ = os.RemoveAll(filepath.Join(root, OldFolder, "заметки"))
	_ = os.RemoveAll(filepath.Join(root, OldFolder, "2026-11-01"))
	_ = os.MkdirAll(filepath.Join(root, OldFolder, "2026-09-02"), 0o755)
	if PruneOld(root, now, 30); exists(filepath.Join(root, OldFolder)) {
		t.Fatal("пустая папка осталась")
	}
	if PruneOld(filepath.Join(root, "нет"), now, 30) != 0 {
		t.Fatal("нет папки")
	}
	// SealAll — только .xlsx.
	sub := filepath.Join(root, OldFolder, "2026-11-07", "X")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "a.xlsx"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(sub, "мой.txt"), []byte("b"), 0o644)
	if n := SealAll(filepath.Join(root, OldFolder), d); n != 1 || opened(t, filepath.Join(sub, "a.xlsx.l7")) != "a" || read(t, sub, "мой.txt") != "b" {
		t.Fatal("SealAll", n)
	}
	if SealAll(root, plain) != 0 {
		t.Fatal("без ключа")
	}
	// CleanupPrefix — и зашифрованные.
	ex := filepath.Join(root, "Excel")
	_ = os.MkdirAll(ex, 0o755)
	_ = os.WriteFile(filepath.Join(ex, "Shop-2026-09-01.xlsx.l7"), []byte("x"), 0o644)
	if CleanupPrefix(ex, "Shop-", "2026-11-08", 30) != 1 {
		t.Fatal("уборка зашифрованных")
	}
}
