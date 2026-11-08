package files

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Те же проверки, что у Sarpo Business 2.1 (SarpoPrint.cs, --self-test): файлы из списка проекта, свои файлы
// человека, переименование, обмен именами, архив, пачки, фото, Excel и уборка старых «что есть сейчас».

type fake struct {
	asked, photos, excel int
	limit                int
	noPhoto              bool
	urls                 []string
	items                func(q url.Values) (any, error)
	backup               func(q url.Values, w io.Writer) (hub.Saved, error)
}

func (f *fake) JSON(_ context.Context, op string, q url.Values, out any) error {
	f.asked++
	f.urls = append(f.urls, op+"?"+q.Encode())
	var v any
	if f.items != nil {
		var err error
		if v, err = f.items(q); err != nil {
			return err
		}
	} else {
		ids := strings.Split(q.Get("ids"), ",")
		var items []map[string]any
		var more []string
		for i, id := range ids {
			if i >= f.limit {
				more = append(more, id)
				continue
			}
			items = append(items, map[string]any{"id": json.Number(id), "files": []map[string]any{
				{"name": "A.xlsx", "data": []byte("id" + id)},
				{"name": `..\evil.xlsx`, "data": []byte("x")},
				{"name": "B.xlsx", "data": []byte("x")},
			}})
		}
		m := []any{}
		for _, x := range more {
			m = append(m, json.Number(x))
		}
		v = map[string]any{"items": items, "more": m}
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, out)
}

func (f *fake) Save(_ context.Context, op string, q url.Values, w io.Writer, _ int64) (hub.Saved, error) {
	switch op {
	case "photo":
		f.photos++
		if f.noPhoto {
			return hub.Saved{}, hub.ErrNothing
		}
		_, _ = w.Write([]byte{1, 2, 3})
		return hub.Saved{Type: "image/jpeg", Bytes: 3}, nil
	case "excel":
		f.excel++
		s := "excel?" + q.Encode()
		_, _ = w.Write([]byte(s))
		return hub.Saved{Type: "application/vnd.ms-excel", Bytes: int64(len(s))}, nil
	case "backup":
		return f.backup(q, w)
	}
	return hub.Saved{}, &hub.APIError{Status: 404, Msg: "нет"}
}

func read(t *testing.T, path ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		return ""
	}
	return string(b)
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestOk(t *testing.T) {
	for _, s := range []string{"Ann", "Ахмед +998901112233", "арт. 0001", "Фото товара", "a.b"} {
		if !Ok(s) {
			t.Errorf("должно подойти: %q", s)
		}
	}
	for _, s := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "end.", "end ", " start", "CON", "com1.txt", "nul", Manifest, BizVer, "~lh-1-x", "a.lh-tmp", strings.Repeat("я", 121), "a\x01"} {
		if Ok(s) {
			t.Errorf("не должно подойти: %q", s)
		}
	}
}

// plainDisk — без пароля копий и без «Старых версий»: как программа 1.2.
var plainDisk = &Disk{}

func TestSyncGroup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := &fake{limit: 25}
	res := &Result{}
	group := func(items ...Item) Group {
		return Group{Kind: "client", Folder: "People", Archive: "People (old)", Files: []string{"A.xlsx"}, Items: items}
	}
	root, old := filepath.Join(dir, "People"), filepath.Join(dir, "People (old)")
	sync := func(g Group) {
		t.Helper()
		if _, err := SyncGroup(ctx, f, 7, dir, plainDisk, g, res, nil); err != nil {
			t.Fatal(err)
		}
	}

	sync(group(Item{ID: 1, Dir: "Ann", Ver: "1"}, Item{ID: 2, Dir: "Bob", Ver: "1"}))
	if f.asked != 1 || read(t, root, "Ann", "A.xlsx") != "id1" || read(t, root, "Bob", "A.xlsx") != "id2" || read(t, root, "Ann", "B.xlsx") != "" || read(t, dir, "evil.xlsx") != "" {
		t.Fatalf("папки: asked=%d", f.asked)
	}
	if f.urls[0] != "items?b=7&ids=1%2C2&kind=client" {
		t.Fatalf("адрес: %s", f.urls[0])
	}
	// Свой файл человека идёт за папкой; имена поменялись местами.
	_ = os.WriteFile(filepath.Join(root, "Ann", "note.txt"), []byte("mine"), 0o644)
	sync(group(Item{ID: 1, Dir: "Bob", Ver: "1"}, Item{ID: 2, Dir: "Ann", Ver: "1"}))
	if f.asked != 1 || read(t, root, "Bob", "A.xlsx") != "id1" || read(t, root, "Bob", "note.txt") != "mine" || read(t, root, "Ann", "A.xlsx") != "id2" || read(t, root, "Ann", "note.txt") != "" {
		t.Fatal("обмен именами")
	}
	// Регистр букв и архив.
	sync(group(Item{ID: 1, Dir: "BOB", Ver: "2"}))
	if f.asked != 2 || !has(names(t, root), "BOB") || read(t, root, "BOB", "note.txt") != "mine" || read(t, old, "Ann", "A.xlsx") != "id2" || has(names(t, root), "Ann") {
		t.Fatalf("архив: %v", names(t, root))
	}
	// Пустой список — похоже на сбой: ничего не убираем.
	sync(group())
	if !has(names(t, root), "BOB") {
		t.Fatal("пустой список убрал папки")
	}
	// Пачки: не поместились — следующим запросом.
	f.limit = 1
	sync(group(Item{ID: 1, Dir: "BOB", Ver: "3"}, Item{ID: 3, Dir: "Cy", Ver: "1"}, Item{ID: 4, Dir: "Di", Ver: "1"}))
	if f.asked != 5 || read(t, root, "Cy", "A.xlsx") != "id3" || read(t, root, "Di", "A.xlsx") != "id4" {
		t.Fatalf("пачки: asked=%d", f.asked)
	}
	// Пропал файл — скачать снова; фото.
	_ = os.Remove(filepath.Join(root, "Di", "A.xlsx"))
	f.limit = 25
	withPhotos := []Item{
		{ID: 1, Dir: "BOB", Ver: "3", Photos: []Photo{{Key: "p", File: "Photo", Ver: "7"}, {Key: "c5", File: "Red", Ver: "8"}, {Key: "../x", File: "Bad", Ver: "1"}}},
		{ID: 3, Dir: "Cy", Ver: "1"}, {ID: 4, Dir: "Di", Ver: "1"},
	}
	sync(group(withPhotos...))
	if f.asked != 6 || read(t, root, "Di", "A.xlsx") != "id4" || f.photos != 2 || read(t, root, "BOB", "Photo.jpg") == "" || read(t, root, "BOB", "Red.jpg") == "" {
		t.Fatalf("фото: asked=%d photos=%d", f.asked, f.photos)
	}
	// Цвет переименовали — файл переименовать, не скачивать.
	withPhotos[0].Photos[1].File = "Crimson"
	sync(group(withPhotos...))
	if f.asked != 6 || f.photos != 2 || read(t, root, "BOB", "Crimson.jpg") == "" || read(t, root, "BOB", "Red.jpg") != "" {
		t.Fatal("переименование фото")
	}
	// Фото у проекта нет — второй раз не спрашиваем.
	f.noPhoto = true
	withPhotos[0].Photos[0].Ver = "9"
	sync(group(withPhotos...))
	sync(group(withPhotos...))
	if f.photos != 3 {
		t.Fatalf("фото без файла спрошено %d раз", f.photos)
	}
	if res.Files == 0 || res.Items == 0 || res.Photos != 2 || len(res.Errors) != 0 {
		t.Fatalf("итог: %+v", res)
	}
	// Служебный файл есть.
	if !has(names(t, root), Manifest) {
		t.Fatal("нет .littlehub.json")
	}
}

func TestSyncGroupBadBatch(t *testing.T) {
	// Ошибка на пачке — по одному: один сломанный не держит остальных.
	dir := t.TempDir()
	f := &fake{limit: 25}
	f.items = func(q url.Values) (any, error) {
		if strings.Contains(q.Get("ids"), "2") {
			return nil, &hub.APIError{Status: 500, Msg: "сбой"}
		}
		return map[string]any{"items": []map[string]any{{"id": json.Number(q.Get("ids")), "files": []map[string]any{{"name": "A.xlsx", "data": []byte("ok")}}}}, "more": []int{}}, nil
	}
	res := &Result{}
	g := Group{Kind: "client", Folder: "P", Archive: "P (a)", Files: []string{"A.xlsx"}, Items: []Item{{ID: 1, Dir: "A", Ver: "1"}, {ID: 2, Dir: "B", Ver: "1"}, {ID: 3, Dir: "C", Ver: "1"}}}
	if _, err := SyncGroup(context.Background(), f, 1, dir, plainDisk, g, res, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "P", "A", "A.xlsx") != "ok" || read(t, dir, "P", "C", "A.xlsx") != "ok" || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "сбой") {
		t.Fatalf("итог: %+v", res)
	}
}

func TestExcel(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := &fake{}
	res := &Result{}
	b := hub.Business{ID: 7, Slug: "shop", Name: "Shop", Kind: "shop", Full: true, Files: []hub.PcFile{
		{Key: "state", Folder: "Excel", Name: "Shop-2026-10-27.xlsx"},
		{Key: "profit", Folder: "", Name: "Profit.xlsx", Refresh: true},
		{Key: "day-2026-10-26", Folder: "Дни", Name: "2026-10-26.xlsx"},
		{Key: "bad", Folder: "..", Name: "x.xlsx"},
		{Key: "bad", Folder: "", Name: "x.exe"},
	}}
	_ = os.MkdirAll(filepath.Join(dir, "Excel"), 0o755)
	for _, n := range []string{"Shop-2026-09-01.xlsx", "Shop-2026-10-20.xlsx", "Other-2026-09-01.xlsx"} {
		_ = os.WriteFile(filepath.Join(dir, "Excel", n), []byte("x"), 0o644)
	}
	if err := Excel(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, true, res); err != nil {
		t.Fatal(err)
	}
	if f.excel != 3 || read(t, dir, "Excel", "Shop-2026-10-27.xlsx") != "excel?b=7&f=state" || read(t, dir, "Profit.xlsx") == "" || read(t, dir, "Дни", "2026-10-26.xlsx") == "" {
		t.Fatalf("Excel: %d", f.excel)
	}
	ex := names(t, filepath.Join(dir, "Excel"))
	if has(ex, "Shop-2026-09-01.xlsx") || !has(ex, "Shop-2026-10-20.xlsx") || !has(ex, "Other-2026-09-01.xlsx") {
		t.Fatalf("уборка: %v", ex)
	}
	// Бизнес не изменился — ничего не качаем.
	if err := Excel(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, false, res); err != nil || f.excel != 3 {
		t.Fatalf("скачано второй раз: %d", f.excel)
	}
	// Изменился — заново «что есть сейчас» и refresh, прошедший день — нет.
	if err := Excel(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, true, res); err != nil || f.excel != 5 {
		t.Fatalf("обновление: %d", f.excel)
	}
}

func TestBusinessVer(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "shop")
	f := &fake{limit: 25}
	f.items = func(q url.Values) (any, error) {
		if q.Get("kind") == "" {
			return map[string]any{"groups": []Group{{Kind: "client", Folder: "Клиенты", Archive: "Клиенты (архив)", Files: []string{"A.xlsx"}, Items: []Item{{ID: 1, Dir: "Ахмед", Ver: "1"}}}}}, nil
		}
		return map[string]any{"items": []map[string]any{{"id": 1, "files": []map[string]any{{"name": "A.xlsx", "data": []byte("a")}}}}, "more": []int{}}, nil
	}
	b := hub.Business{ID: 7, Slug: "shop", Name: "Shop", Ver: "v1", Files: []hub.PcFile{{Key: "state", Folder: "Excel", Name: "Shop-2026-10-27.xlsx"}}}
	res := &Result{}
	if err := Business(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, false, res, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "Клиенты", "Ахмед", "A.xlsx") != "a" || ReadVer(dir) != "v1" || res.Businesses != 1 {
		t.Fatalf("бизнес: %+v", res)
	}
	asked, excel := f.asked, f.excel
	// Та же версия — проект не спрашиваем.
	if err := Business(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, false, res, nil); err != nil || f.asked != asked || f.excel != excel {
		t.Fatal("та же версия — спросили снова")
	}
	b.Ver = "v2"
	if err := Business(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, false, res, nil); err != nil || f.asked == asked || f.excel != excel+1 || ReadVer(dir) != "v2" {
		t.Fatal("новая версия — не спросили")
	}
	// Связь пропала — ошибка наверх, версия не записывается.
	f.items = func(url.Values) (any, error) { return nil, hub.ErrRevoked }
	b.Ver = "v3"
	if err := Business(ctx, f, b, dir, &Disk{Today: "2026-10-27"}, false, res, nil); err == nil || ReadVer(dir) != "v2" {
		t.Fatal("ошибка связи проглочена")
	}
}

func TestProjectCopy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := days.Project{Slug: "sarpo", Folder: "Sarpo Business", Ext: "l7copy"}
	// Похоже на настоящую копию: метка, соль, повторы (1000), IV, блоки и HMAC.
	body := append([]byte("L7-DB-1\n"), make([]byte, 16)...)
	body = append(body, 0, 0, 3, 0xe8)
	body = append(body, make([]byte, 16+32+32)...)
	f := &fake{}
	var sent url.Values
	f.backup = func(q url.Values, w io.Writer) (hub.Saved, error) {
		sent = q
		if q.Get("have") == "sarpo-2026-11-07.sql.gz" {
			return hub.Saved{}, hub.ErrNothing
		}
		n, _ := io.Copy(w, bytes.NewReader(body))
		h := map[string][]string{"X-Copy-Name": {"sarpo-2026-11-07.sql.gz"}, "X-Copy-Day": {"2026-11-07"}, "X-Copy-Bytes": {strconv.Itoa(len(body))}}
		return hub.Saved{Bytes: n, Header: h}, nil
	}
	c, err := ProjectCopy(ctx, f, p, root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Sarpo Business", "Копии базы", "2026-11", "sarpo-2026-11-07.l7copy")
	if c.Path != want || c.Name != "sarpo-2026-11-07.sql.gz" || c.Day != "2026-11-07" || read(t, want) != string(body) {
		t.Fatalf("копия: %+v", c)
	}
	if _, err := ProjectCopy(ctx, f, p, root, c.Name, c.Path); err != hub.ErrNothing || sent.Get("have") != c.Name {
		t.Fatalf("та же копия: %v", err)
	}
	// Файла нет на диске (сменили диск) — have не шлём, качаем снова.
	if _, err := ProjectCopy(ctx, f, p, root, c.Name, filepath.Join(root, "нет")); err != nil || sent.Get("have") != "" {
		t.Fatalf("копия на новый диск: %v", err)
	}
	// Не целая — не ставим.
	f.backup = func(q url.Values, w io.Writer) (hub.Saved, error) {
		n, _ := io.Copy(w, bytes.NewReader(body[:40]))
		return hub.Saved{Bytes: n, Header: map[string][]string{"X-Copy-Name": {"sarpo-2026-11-08.sql.gz"}, "X-Copy-Day": {"2026-11-08"}, "X-Copy-Bytes": {strconv.Itoa(len(body))}}}, nil
	}
	if _, err := ProjectCopy(ctx, f, p, root, "", ""); err == nil || days.Exists(filepath.Join(root, "Sarpo Business", "Копии базы", "2026-11", "sarpo-2026-11-08.l7copy")) {
		t.Fatal("неполная копия поставилась")
	}
	if left := names(t, filepath.Join(root, "Sarpo Business", "Копии базы")); len(left) != 2 || left[0] != days.ShaFile || !regexp.MustCompile(`^2026-11$`).MatchString(left[1]) {
		t.Fatalf("остался временный файл: %v", left)
	}
}

func TestMoveInto(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "Ann"), filepath.Join(dir, "old", "Ann")
	_ = os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	_ = os.MkdirAll(dst, 0o755)
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644)
	_ = os.WriteFile(filepath.Join(dst, "a.txt"), []byte("old"), 0o644)
	if err := MoveInto(src, dst); err != nil {
		t.Fatal(err)
	}
	// Тёзка в архиве: новые файлы — рядом, в «Ann (2)»; чужих нет — внутрь.
	if read(t, dst, "a.txt") != "old" || read(t, dst, "sub", "b.txt") != "b" || read(t, dir, "old", "Ann (2)", "a.txt") != "new" || exists(src) {
		t.Fatalf("перенос: %v", names(t, filepath.Join(dir, "old")))
	}
	if n := CleanupPrefix(dir, "", "2026-11-08", 30); n != 0 {
		t.Fatal("пустой префикс")
	}
	if photoExt("image/png; q=1") != ".png" || photoExt("image/webp") != ".webp" || photoExt("image/gif") != ".gif" || photoExt("") != ".jpg" {
		t.Fatal("расширение фото")
	}
	r := &Result{}
	for i := 0; i < 30; i++ {
		r.Fail("x", os.ErrNotExist)
	}
	if len(r.Errors) != maxErrs || r.failed != 30 {
		t.Fatal("ошибок не больше 20")
	}
	if Fatal(nil) || Fatal(&hub.APIError{Status: 500}) || Fatal(&os.PathError{Op: "open", Err: os.ErrPermission}) || !Fatal(hub.ErrRevoked) {
		t.Fatal("какие ошибки останавливают")
	}
}
