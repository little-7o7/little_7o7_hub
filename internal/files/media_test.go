package files

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Архив проекта, оригиналы фото и «Фото базы» (план pc-backup-2, этап 3).

func TestSyncGroupArchive(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := &fake{limit: 25}
	res := &Result{}
	root, old := filepath.Join(dir, "People"), filepath.Join(dir, "People (old)")
	sync := func(items ...Item) {
		t.Helper()
		g := Group{Kind: "client", Folder: "People", Archive: "People (old)", Files: []string{"A.xlsx"}, Items: items}
		if _, err := SyncGroup(ctx, f, 7, dir, plainDisk, g, res, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Архивный у проекта — сразу в архив; тёзки в папке вида и в архиве — оба на месте.
	sync(Item{ID: 1, Dir: "Ann", Ver: "1"}, Item{ID: 2, Dir: "Ann", Ver: "1", Arch: true}, Item{ID: 3, Dir: "Bob", Ver: "1"})
	if read(t, root, "Ann", "A.xlsx") != "id1" || read(t, old, "Ann", "A.xlsx") != "id2" || read(t, root, "Bob", "A.xlsx") != "id3" {
		t.Fatalf("архивный: %v / %v", names(t, root), names(t, old))
	}
	// Убрали в архив — папка со своими файлами человека переезжает, заново не качаем.
	_ = os.WriteFile(filepath.Join(root, "Bob", "note.txt"), []byte("mine"), 0o644)
	asked := f.asked
	sync(Item{ID: 1, Dir: "Ann", Ver: "1"}, Item{ID: 2, Dir: "Ann", Ver: "1", Arch: true}, Item{ID: 3, Dir: "Bob", Ver: "1", Arch: true})
	if f.asked != asked || has(names(t, root), "Bob") || read(t, old, "Bob", "note.txt") != "mine" || read(t, old, "Bob", "A.xlsx") != "id3" {
		t.Fatalf("в архив: asked %d→%d, %v / %v", asked, f.asked, names(t, root), names(t, old))
	}
	// Вернули из архива и переименовали — обратно, под новым именем.
	sync(Item{ID: 1, Dir: "Ann", Ver: "1"}, Item{ID: 2, Dir: "Ann", Ver: "1", Arch: true}, Item{ID: 3, Dir: "Bobur", Ver: "1"})
	if read(t, root, "Bobur", "note.txt") != "mine" || read(t, root, "Bobur", "A.xlsx") != "id3" || has(names(t, old), "Bob") {
		t.Fatalf("из архива: %v / %v", names(t, root), names(t, old))
	}
	// Удалили у проекта архивного — папка остаётся в архиве.
	sync(Item{ID: 1, Dir: "Ann", Ver: "1"}, Item{ID: 3, Dir: "Bobur", Ver: "1"})
	if read(t, old, "Ann", "A.xlsx") != "id2" || read(t, root, "Ann", "A.xlsx") != "id1" {
		t.Fatalf("удалённый архивный: %v / %v", names(t, root), names(t, old))
	}
	if len(res.Errors) != 0 {
		t.Fatal(res.Errors)
	}
}

func TestPhotoOriginal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := &fake{limit: 25}
	res := &Result{}
	root := filepath.Join(dir, "Товары")
	g := Group{Kind: "product", Folder: "Товары", Archive: "Товары (архив)", Files: []string{"A.xlsx"}, Items: []Item{
		{ID: 4, Dir: "Ткань", Ver: "1", Photos: []Photo{{Key: "p", File: "Фото товара", Ver: "1"}}},
	}}
	// Старое фото (до оригиналов) — JPG прямо от проекта.
	if _, err := SyncGroup(ctx, f, 7, dir, plainDisk, g, res, nil); err != nil {
		t.Fatal(err)
	}
	if f.photos != 1 || read(t, root, "Ткань", "Фото товара.jpg") != "\x01\x02\x03" {
		t.Fatal("старое фото")
	}
	// Новое фото: проект даёт ссылку на оригинал (PNG) — качаем из хранилища, старый JPG убираем.
	f.link = "image/png"
	g.Items[0].Photos[0].Ver = "2"
	if _, err := SyncGroup(ctx, f, 7, dir, plainDisk, g, res, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "Ткань", "Фото товара.png") != "orig 4-" || read(t, root, "Ткань", "Фото товара.jpg") != "" || len(f.fetched) != 1 {
		t.Fatalf("оригинал: %v, %v", names(t, filepath.Join(root, "Ткань")), f.fetched)
	}
	// Хранилище не отдало — ошибка в итоге, версия не запоминается: в другой раз ещё раз.
	g.Items[0].Photos[0].Ver = "3"
	f.link = "image/webp"
	if _, err := SyncGroup(ctx, &failFetch{f}, 7, dir, plainDisk, g, res, nil); err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || read(t, root, "Ткань", "Фото товара.png") != "orig 4-" {
		t.Fatalf("ошибка хранилища: %v", res.Errors)
	}
	if _, err := SyncGroup(ctx, f, 7, dir, plainDisk, g, res, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "Ткань", "Фото товара.webp") != "orig 4-" || read(t, root, "Ткань", "Фото товара.png") != "" {
		t.Fatalf("повтор: %v", names(t, filepath.Join(root, "Ткань")))
	}
	if q := f.urls; !strings.Contains(strings.Join(q, " "), "items?b=7&ids=4&kind=product") {
		t.Fatal(q)
	}
}

// failFetch — хранилище не отвечает.
type failFetch struct{ *fake }

func (f *failFetch) Fetch(context.Context, string, io.Writer, int64) (hub.Saved, error) {
	return hub.Saved{}, &hub.APIError{Status: 403, Msg: "срок ссылки вышел"}
}

type mediaSrc struct {
	*fake
	list []map[string]any
	page int
}

func newMedia(n int) *mediaSrc {
	m := &mediaSrc{fake: &fake{blobs: map[string][]byte{}}, page: 2}
	for i := 1; i <= n; i++ {
		m.add(i, "Чеки операций/2026-11", "2026-11-0"+strconv.Itoa(i%9+1)+" Расход №"+strconv.Itoa(i)+".jpg")
	}
	m.fake.media = func(q url.Values) (any, error) {
		after, _ := strconv.Atoi(q.Get("after"))
		var out []map[string]any
		for _, x := range m.list {
			if x["id"].(int) > after && len(out) < m.page+1 {
				out = append(out, x)
			}
		}
		var next any
		if len(out) > m.page {
			out = out[:m.page]
			next = out[len(out)-1]["id"]
		}
		if out == nil {
			out = []map[string]any{}
		}
		return map[string]any{"files": out, "next": next}, nil
	}
	return m
}

func (m *mediaSrc) add(id int, dir, name string) {
	u := "https://s.private.blob.vercel-storage.com/a/" + strconv.Itoa(id)
	m.blobs[u] = []byte("фото " + strconv.Itoa(id))
	m.list = append(m.list, map[string]any{"id": id, "dir": dir, "name": name, "url": u, "blob": "a/" + strconv.Itoa(id), "what": "Чек №" + strconv.Itoa(id), "at": "2026-11-08T09:05:00.000Z", "deleted": nil})
}

func TestMedia(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := newMedia(5)
	d := &Disk{Root: dir, Today: "2026-11-08"}
	res := &Result{}
	run := func(force bool) {
		t.Helper()
		if err := Media(ctx, src, 7, dir, d, force, res, nil); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, MediaFolder, "Чеки операций", "2026-11")
	run(false)
	if res.Media != 5 || read(t, root, "2026-11-04 Расход №3.jpg") != "фото 3" || len(src.fetched) != 5 {
		t.Fatalf("первый раз: %+v %v", res, names(t, root))
	}
	csv := read(t, dir, MediaFolder, MediaList)
	if !strings.HasPrefix(csv, "\xef\xbb\xbfФайл;Что это;") || !strings.Contains(csv, "Чек №3;2026-11-08 14:05;;;a/3") {
		t.Fatalf("список: %q", csv)
	}
	// Тот же день: спрашиваем только новые (after — последний id), качаем только новый.
	src.add(6, "Поломки в номерах/2026-11", "2026-11-08 номер 12 кран №6.jpg")
	src.fetched = nil
	before := len(src.urls)
	run(false)
	if len(src.fetched) != 1 || src.urls[before] != "media?after=5&b=7" || read(t, dir, MediaFolder, "Поломки в номерах", "2026-11", "2026-11-08 номер 12 кран №6.jpg") != "фото 6" {
		t.Fatalf("новые: %v %v", src.fetched, src.urls[before:])
	}
	// «Сохранить сейчас»: весь список — переименованное переименовать, стёртое в хранилище отметить, файл остаётся.
	src.list[0]["name"] = "2026-11-02 Расход аренда №1.jpg"
	src.list = src.list[:len(src.list)-1]
	src.list[1]["deleted"] = "2026-11-08T10:00:00Z"
	src.fetched = nil
	run(true)
	if len(src.fetched) != 0 || read(t, root, "2026-11-02 Расход аренда №1.jpg") != "фото 1" || read(t, root, "2026-11-02 Расход №1.jpg") != "" {
		t.Fatalf("переименование: %v %v", src.fetched, names(t, root))
	}
	csv = read(t, dir, MediaFolder, MediaList)
	if !strings.Contains(csv, "№6.jpg;Чек №6;2026-11-08 14:05;;2026-11-08;a/6") || !strings.Contains(csv, "Чек №2;2026-11-08 14:05;2026-11-08 15:00;;a/2") {
		t.Fatalf("отметки: %q", csv)
	}
	if read(t, dir, MediaFolder, "Поломки в номерах", "2026-11", "2026-11-08 номер 12 кран №6.jpg") != "фото 6" {
		t.Fatal("стёртое в хранилище убрали с диска")
	}
	// Чужие пути — мимо; файл не скачался — в другой раз ещё раз, с него.
	src.add(7, "../x", "a.jpg")
	src.add(8, "Другие/2026-11", "CON.jpg")
	src.add(9, "Другие/2026-11", "2026-11-08 №9.jpg")
	delete(src.blobs, src.list[len(src.list)-1]["url"].(string))
	src.add(10, "Другие/2026-11", "2026-11-08 №10.jpg")
	src.fetched = nil
	errs := len(res.Errors)
	run(false)
	if len(res.Errors) != errs+1 || read(t, dir, MediaFolder, "Другие", "2026-11", "2026-11-08 №10.jpg") != "фото 10" || len(names(t, filepath.Join(dir, "x"))) != 0 {
		t.Fatalf("плохие: %v", res.Errors)
	}
	src.blobs[src.list[len(src.list)-2]["url"].(string)] = []byte("фото 9")
	before = len(src.urls)
	run(false)
	if src.urls[before] != "media?after=8&b=7" || read(t, dir, MediaFolder, "Другие", "2026-11", "2026-11-08 №9.jpg") != "фото 9" {
		t.Fatalf("повтор: %v", src.urls[before:])
	}
}

func TestMediaSealedAndMissing(t *testing.T) {
	ctx := context.Background()
	// У проекта «Фото базы» нет (Strassio, старый Sarpo) — ничего, без ошибки и без папки.
	dir := t.TempDir()
	res := &Result{}
	if err := Media(ctx, &fake{}, 1, dir, plainDisk, false, res, nil); err != nil || len(res.Errors) != 0 || exists(filepath.Join(dir, MediaFolder)) {
		t.Fatal(err, res.Errors)
	}
	// Под паролем копий — файлы и список зашифрованы.
	lock := testLock(t)
	src := newMedia(2)
	d := &Disk{Root: dir, Today: "2026-11-08", Lock: lock}
	if err := Media(ctx, src, 7, dir, d, false, res, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, MediaFolder)
	if !isFile(filepath.Join(root, MediaList+fileseal.Ext)) || isFile(filepath.Join(root, MediaList)) ||
		!isFile(filepath.Join(root, "Чеки операций", "2026-11", "2026-11-02 Расход №1.jpg"+fileseal.Ext)) {
		t.Fatal(names(t, root))
	}
}
