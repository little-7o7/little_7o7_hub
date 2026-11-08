// Package files — файлы проектов на дисках владельца (2026-11-08, владелец: «nujen vse fayli proyektov, vsex
// klientov», «v sarpo toje. dlya klientov, postavshikov, masterov otdelniye papki. dlya tovarov yesho papki, s
// informatsiyami i fotografiyami. kak sarpobusiness v starom versii»). То же, что делала программа магазина Sarpo
// Business 2.1 (SarpoPrint.cs, класс Files), но для всех проектов хаба:
//
//	<диск>/Little Hub/Sarpo Business/<бизнес>/  — Excel («что есть сейчас», дни, месяцы, прибыль),
//	                                             Товары/<товар>/ (о товаре, фото товара и цветов),
//	                                             Клиенты/, Поставщики/, Мастера/ — папка на каждого,
//	                                             «… (архив)» — кого больше нет в списке.
//
// Что и куда класть, решает проект; программа скачивает только новое и изменённое: версии папок хранит в скрытом
// .littlehub.json, версию бизнеса — в .littlehub-ver (не сменилась — бизнес не спрашиваем). Человек кладёт в папку
// клиента свои файлы — они остаются: программа меняет только файлы из списка проекта, а папку переименовывает и
// убирает в архив целиком.
package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

const (
	Manifest = ".littlehub.json"
	BizVer   = ".littlehub-ver"
	Batch    = 25
	KeepDays = 30
	MaxExcel = 200 << 20
	MaxPhoto = 50 << 20
	tmp      = ".lh-tmp"
	moving   = "~lh-"
	maxErrs  = 20
)

// Result — что вышло: записано файлов, папок, фото; locked — файл открыт (в Excel), не заменён.
type Result struct {
	Businesses int      `json:"businesses"`
	Files      int      `json:"files"`
	Items      int      `json:"items"`
	Photos     int      `json:"photos"`
	Locked     int      `json:"locked"`
	Errors     []string `json:"errors"`
	failed     int
}

func (r *Result) Fail(what string, err error) {
	r.failed++
	if len(r.Errors) >= maxErrs {
		return
	}
	s := what + ": " + err.Error()
	if utf8.RuneCountInString(s) > 290 {
		s = string([]rune(s)[:290]) + "…"
	}
	r.Errors = append(r.Errors, s)
}

// Source — ответы одного проекта (через хаб). В проверках — подставной.
type Source interface {
	JSON(ctx context.Context, op string, q url.Values, out any) error
	Save(ctx context.Context, op string, q url.Values, w io.Writer, max int64) (hub.Saved, error)
}

type project struct {
	c    *hub.Client
	slug string
}

func (p project) JSON(ctx context.Context, op string, q url.Values, out any) error {
	return p.c.ProjectJSON(ctx, p.slug, op, q, out)
}
func (p project) Save(ctx context.Context, op string, q url.Values, w io.Writer, max int64) (hub.Saved, error) {
	return p.c.ProjectSave(ctx, p.slug, op, q, w, max)
}

// Of — ответы проекта slug через хаб.
func Of(c *hub.Client, slug string) Source { return project{c, slug} }

// Fatal — ошибка, после которой дальше нет смысла: нет связи, компьютер отключён. Ответ с ошибкой и диск — нет.
func Fatal(err error) bool {
	if err == nil || hub.IsAPI(err) || errors.Is(err, hub.ErrNothing) {
		return false
	}
	var pe *fs.PathError
	var le *os.LinkError
	return !errors.As(err, &pe) && !errors.As(err, &le)
}

var (
	reserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\..*)?$`)
	SlugRe   = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	photoKey = regexp.MustCompile(`^(p|c\d{1,9})$`)
	dated    = regexp.MustCompile(`^(.+-)\d{4}-\d{2}-\d{2}\.xlsx$`)
)

// Ok — имя от проекта годится для файла или папки Windows и Mac: не пустое, без \ / : * ? " < > | и управляющих
// знаков, без точки и пробела в конце и пробела в начале, не CON, PRN…, не длиннее 120 знаков, не служебное.
func Ok(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > 120 || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if c < 32 || c == 127 || strings.ContainsRune(`<>:"/\|?*`, c) {
			return false
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.HasPrefix(name, " ") || reserved.MatchString(name) {
		return false
	}
	return name != Manifest && name != BizVer && !strings.HasPrefix(name, moving) && !strings.HasSuffix(name, tmp)
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// put — готовый временный файл на место path целиком. Файл открыт (в Excel) — false: старый остаётся, временный
// удаляем.
func put(temp, path string) bool {
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return false
	}
	return true
}

func writeTemp(path string, data []byte) (string, error) {
	t := path + tmp
	f, err := os.OpenFile(t, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(t)
		return "", err
	}
	return t, nil
}

// write — файл целиком: true — записан, false — открыт и не заменён.
func write(path string, data []byte) (bool, error) {
	t, err := writeTemp(path, data)
	if err != nil {
		return false, err
	}
	return put(t, path), nil
}

// writeHidden — служебный файл (скрытый на Windows: снять «скрытый», заменить, поставить снова).
func writeHidden(path string, data []byte) error {
	unhide(path)
	t, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	if err := os.Rename(t, path); err != nil {
		_ = os.Remove(t)
		return err
	}
	hide(path)
	return nil
}

// save — файл проекта во временный рядом и на место. ErrNothing — у проекта его нет.
func save(ctx context.Context, src Source, op string, q url.Values, path string, max int64) (hub.Saved, bool, error) {
	t := path + tmp
	f, err := os.OpenFile(t, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return hub.Saved{}, false, err
	}
	s, err := src.Save(ctx, op, q, f, max)
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(t)
		return s, false, err
	}
	return s, put(t, path), nil
}

// MoveInto — папку src в dst. dst уже есть (тёзка в архиве, своя папка человека) — переносим по файлу; что не
// поместилось (такие же имена), остаётся рядом, в «dst (2)».
func MoveInto(src, dst string) error {
	if !exists(dst) {
		return os.Rename(src, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, t := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := MoveInto(s, t); err != nil {
				return err
			}
		} else if !exists(t) {
			if err := os.Rename(s, t); err != nil {
				return err
			}
		}
	}
	if left, _ := os.ReadDir(src); len(left) == 0 {
		return os.Remove(src)
	}
	free := dst + " (2)"
	for n := 3; exists(free); n++ {
		free = fmt.Sprintf("%s (%d)", dst, n)
	}
	return os.Rename(src, free)
}

// ---------- Бизнес ----------

// ReadVer — версия бизнеса, с которой папка уже полностью совпадает; нет — "".
func ReadVer(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, BizVer))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Business — Excel, папки и фото одного бизнеса в dir. Версия не сменилась с прошлого раза (всё тогда записалось) —
// ничего не спрашиваем. Ошибки ответов и диска — в res, дальше; нет связи или компьютер отключён — ошибка.
func Business(ctx context.Context, src Source, b hub.Business, dir, today string, res *Result, progress func(string)) error {
	name := b.Name
	if name == "" {
		name = b.Slug
	}
	prev := ReadVer(dir)
	if prev != "" && prev == b.Ver {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		res.Fail(name, err)
		return nil
	}
	before := res.failed + res.Locked
	if err := Excel(ctx, src, b, dir, today, prev != b.Ver, res); err != nil {
		return err
	}
	var groups struct {
		Groups []Group `json:"groups"`
	}
	if err := src.JSON(ctx, "items", url.Values{"b": {strconv.Itoa(b.ID)}}, &groups); err != nil {
		if Fatal(err) {
			return err
		}
		res.Fail(name, err)
		return nil
	}
	for _, g := range groups.Groups {
		if g.Kind == "" || g.Files == nil || !Ok(g.Folder) || !Ok(g.Archive) {
			continue
		}
		step := func(s string) {
			if progress != nil {
				progress(name + ": " + s)
			}
		}
		if _, err := SyncGroup(ctx, src, b.ID, dir, g, res, step); err != nil {
			if Fatal(err) {
				return err
			}
			res.Fail(name+", "+g.Folder, err)
		}
	}
	res.Businesses++
	if res.failed+res.Locked == before && b.Ver != "" {
		if err := writeHidden(filepath.Join(dir, BizVer), []byte(b.Ver)); err != nil {
			res.Fail(name, err)
		}
	}
	return nil
}

// Excel — файлы Excel бизнеса. Нет — скачать. Бизнес изменился (changed) — заново «что есть сейчас» и файлы с
// refresh (текущий месяц, прибыль). Прошедшие дни и месяцы — только если файла нет. «Что есть сейчас» за прошлые дни
// хранится 30 дней, как в Sarpo Business.
func Excel(ctx context.Context, src Source, b hub.Business, dir, today string, changed bool, res *Result) error {
	for _, f := range b.Files {
		if f.Key == "" || !Ok(f.Name) || !strings.HasSuffix(strings.ToLower(f.Name), ".xlsx") || !(f.Folder == "" || Ok(f.Folder)) {
			continue
		}
		folder := dir
		if f.Folder != "" {
			folder = filepath.Join(dir, f.Folder)
		}
		path := filepath.Join(folder, f.Name)
		if isFile(path) && !(changed && (f.Refresh || f.Key == "state")) {
			continue
		}
		if err := os.MkdirAll(folder, 0o755); err != nil {
			res.Fail(f.Name, err)
			continue
		}
		_, ok, err := save(ctx, src, "excel", url.Values{"b": {strconv.Itoa(b.ID)}, "f": {f.Key}}, path, MaxExcel)
		switch {
		case errors.Is(err, hub.ErrNothing):
		case err != nil && Fatal(err):
			return err
		case err != nil:
			res.Fail(f.Name, err)
		case ok:
			res.Files++
		default:
			res.Locked++
		}
	}
	for _, f := range b.Files {
		if f.Key != "state" || !Ok(f.Name) || !(f.Folder == "" || Ok(f.Folder)) {
			continue
		}
		if m := dated.FindStringSubmatch(f.Name); m != nil {
			folder := dir
			if f.Folder != "" {
				folder = filepath.Join(dir, f.Folder)
			}
			CleanupPrefix(folder, m[1], today, KeepDays)
		}
	}
	return nil
}

// CleanupPrefix — файлы prefix+ГГГГ-ММ-ДД.xlsx старше days дней; другие не трогаем.
func CleanupPrefix(dir, prefix, today string, days int) int {
	t, err := time.Parse("2006-01-02", today)
	if prefix == "" || err != nil {
		return 0
	}
	re, err := regexp.Compile(`(?i)^` + regexp.QuoteMeta(prefix) + `(\d{4}-\d{2}-\d{2})\.xlsx$`)
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		d, err := time.Parse("2006-01-02", m[1])
		if err != nil || t.Sub(d) <= time.Duration(days)*24*time.Hour {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}

// ---------- Папки ----------

type Photo struct {
	Key  string `json:"key"`
	File string `json:"file"`
	Ver  string `json:"ver"`
}

type Item struct {
	ID     int     `json:"id"`
	Dir    string  `json:"dir"`
	Ver    string  `json:"ver"`
	Photos []Photo `json:"photos,omitempty"`
}

// Group — папки одного вида: «Товары», «Клиенты», «Поставщики», «Мастера»; files — какие файлы проект кладёт в папку.
type Group struct {
	Kind    string   `json:"kind"`
	Folder  string   `json:"folder"`
	Archive string   `json:"archive"`
	Files   []string `json:"files"`
	Items   []Item   `json:"items"`
}

// Kept — что лежит в папке: имя, версия, файлы проекта, фото (ключ → «версия|файл»).
type Kept struct {
	Dir    string            `json:"dir"`
	Ver    string            `json:"ver"`
	Files  []string          `json:"files,omitempty"`
	Photos map[string]string `json:"photos,omitempty"`
}

func loadKept(path string) map[string]*Kept {
	out := map[string]*Kept{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var d map[string]*Kept
	if json.Unmarshal(b, &d) != nil {
		return out
	}
	for k, v := range d {
		if v != nil && Ok(v.Dir) {
			out[k] = v
		}
	}
	return out
}

func saveKept(path string, kept map[string]*Kept) error {
	b, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return writeHidden(path, b)
}

type itemFile struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type itemData struct {
	ID    int        `json:"id"`
	Files []itemFile `json:"files"`
}

type itemAnswer struct {
	Items []itemData `json:"items"`
	More  []int      `json:"more"`
}

func photoExt(t string) string {
	t = strings.ToLower(strings.TrimSpace(strings.Split(t, ";")[0]))
	switch t {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".jpg"
}

func randHex() string {
	return strconv.FormatInt(time.Now().UnixNano()%1e9, 16)
}

// SyncGroup — папки одного вида: кого нет в списке — в архив; переименованные — переименовать; новые и изменённые
// (другая версия, пропал файл) — скачать пачками по 25; фото — по одному. Ответ — что лежит в папках.
func SyncGroup(ctx context.Context, src Source, biz int, dir string, g Group, res *Result, progress func(string)) (map[string]*Kept, error) {
	root, archive := filepath.Join(dir, g.Folder), filepath.Join(dir, g.Archive)
	manifest := filepath.Join(root, Manifest)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	kept := loadKept(manifest)
	key := strconv.Itoa
	allowed := map[string]bool{}
	for _, f := range g.Files {
		allowed[f] = true
	}
	// Список проекта: проверенные имена, без повторов (Windows и Mac не различают большие и маленькие буквы).
	var items []Item
	byID := map[string]Item{}
	names := map[string]bool{}
	for _, it := range g.Items {
		low := strings.ToLower(it.Dir)
		if it.ID <= 0 || !Ok(it.Dir) || byID[key(it.ID)].ID != 0 || names[low] {
			continue
		}
		names[low] = true
		items = append(items, it)
		byID[key(it.ID)] = it
	}
	// 1. Кого больше нет (в архиве у проекта, удалён) — папку целиком в «… (архив)». Пустой список при прежних папках
	// похож на сбой: ничего не убираем.
	if len(items) > 0 {
		for id, k := range kept {
			if _, ok := byID[id]; ok {
				continue
			}
			from := filepath.Join(root, k.Dir)
			if isDir(from) {
				if os.MkdirAll(archive, 0o755) != nil || MoveInto(from, filepath.Join(archive, k.Dir)) != nil {
					continue
				}
			}
			delete(kept, id)
		}
	}
	// 2. Переименованные — через временное имя: так два имени меняются местами и меняется только регистр букв. Не
	// вышло (внутри открыт файл) — папка остаётся под прежним именем, новые файлы — в неё, переименуем в другой раз.
	renames := map[string]string{}
	for _, it := range items {
		k, ok := kept[key(it.ID)]
		if !ok || k.Dir == it.Dir {
			continue
		}
		from := filepath.Join(root, k.Dir)
		if !isDir(from) {
			k.Dir = it.Dir
			continue
		}
		temp := fmt.Sprintf("%s%d-%s", moving, it.ID, randHex())
		if os.Rename(from, filepath.Join(root, temp)) == nil {
			k.Dir = temp
			renames[key(it.ID)] = it.Dir
		}
	}
	for id, to := range renames {
		k := kept[id]
		if MoveInto(filepath.Join(root, k.Dir), filepath.Join(root, to)) == nil {
			k.Dir = to
		}
	}
	if err := saveKept(manifest, kept); err != nil {
		return kept, err
	}
	// 3. Что скачать: новые, другая версия, пропал файл, который программа клала.
	var fetch []int
	for _, it := range items {
		k, ok := kept[key(it.ID)]
		if !ok {
			k = &Kept{Dir: it.Dir}
			kept[key(it.ID)] = k
		}
		if err := os.MkdirAll(filepath.Join(root, k.Dir), 0o755); err != nil {
			res.Fail(g.Folder+" "+k.Dir, err)
			continue
		}
		missing := false
		for _, f := range k.Files {
			if !Ok(f) || !isFile(filepath.Join(root, k.Dir, f)) {
				missing = true
			}
		}
		if k.Ver != it.Ver || missing {
			fetch = append(fetch, it.ID)
		}
	}
	// 4. Пачками. more — не поместились в ответ, просим следующим запросом. Ошибка на пачке — просим по одному, чтобы
	// один сломанный не держал остальных.
	queue := append([]int(nil), fetch...)
	alone := map[int]bool{}
	done := 0
	for len(queue) > 0 {
		take := min(Batch, len(queue))
		if alone[queue[0]] {
			take = 1
		}
		batch := append([]int(nil), queue[:take]...)
		queue = queue[take:]
		ids := make([]string, len(batch))
		in := map[int]bool{}
		for i, id := range batch {
			ids[i] = key(id)
			in[id] = true
		}
		var a itemAnswer
		err := src.JSON(ctx, "items", url.Values{"b": {key(biz)}, "kind": {g.Kind}, "ids": {strings.Join(ids, ",")}}, &a)
		if err != nil {
			if Fatal(err) {
				return kept, err
			}
			if len(batch) > 1 {
				for _, id := range batch {
					alone[id] = true
				}
				queue = append(batch, queue...)
			} else {
				res.Fail(g.Folder+" "+byID[key(batch[0])].Dir, err)
			}
			continue
		}
		got, more := map[int]bool{}, map[int]bool{}
		for _, id := range a.More {
			more[id] = true
		}
		for _, d := range a.Items {
			if !in[d.ID] || got[d.ID] {
				continue
			}
			got[d.ID] = true
			k := kept[key(d.ID)]
			folder := filepath.Join(root, k.Dir)
			var written []string
			all := true
			for _, f := range d.Files {
				if !Ok(f.Name) || !allowed[f.Name] {
					continue
				}
				ok, err := write(filepath.Join(folder, f.Name), f.Data)
				switch {
				case err != nil:
					all = false
					res.Fail(g.Folder+" "+k.Dir, err)
				case ok:
					written = append(written, f.Name)
					res.Files++
				default:
					all = false
					res.Locked++
				}
			}
			if all {
				k.Ver, k.Files = byID[key(d.ID)].Ver, written
			}
			res.Items++
		}
		var again []int
		for _, id := range batch {
			if got[id] {
				continue
			}
			if more[id] {
				again = append(again, id)
			} else {
				k := kept[key(id)] // проекту нечего положить
				k.Ver, k.Files = byID[key(id)].Ver, nil
			}
		}
		if len(got) == 0 && len(again) > 0 {
			return kept, &hub.APIError{Status: 502, Msg: "проект не отдал файлы папок «" + g.Folder + "»"}
		}
		queue = append(again, queue...)
		done += len(batch) - len(again)
		if progress != nil {
			progress(fmt.Sprintf("%s %d / %d", g.Folder, done, len(fetch)))
		}
		if err := saveKept(manifest, kept); err != nil {
			return kept, err
		}
	}
	// 5. Фото (имя — товар или цвет). То же фото под другим именем (цвет переименовали) — переименовать.
	asked := 0
	for _, it := range items {
		if len(it.Photos) == 0 {
			continue
		}
		k := kept[key(it.ID)]
		folder := filepath.Join(root, k.Dir)
		if k.Photos == nil {
			k.Photos = map[string]string{}
		}
		keys := map[string]bool{}
		written := map[string]bool{} // записанные сейчас: цвета поменялись именами — старому файлу с таким именем не верим
		for _, p := range it.Photos {
			if p.Key == "" || p.Ver == "" || !photoKey.MatchString(p.Key) || !Ok(p.File) || keys[p.Key] {
				continue
			}
			keys[p.Key] = true
			oldFile := ""
			if was, ok := k.Photos[p.Key]; ok {
				ver, file, found := strings.Cut(was, "|")
				if found {
					oldFile = file
					if ver == p.Ver {
						if oldFile == "" {
							continue // фото у проекта нет — не спрашиваем, пока не сменится версия
						}
						oldPath, want := filepath.Join(folder, oldFile), p.File+filepath.Ext(oldFile)
						if Ok(oldFile) && isFile(oldPath) && !written[strings.ToLower(oldFile)] {
							if oldFile == want {
								continue
							}
							if !isFile(filepath.Join(folder, want)) || strings.EqualFold(want, oldFile) {
								if os.Rename(oldPath, filepath.Join(folder, want)) == nil {
									k.Photos[p.Key] = p.Ver + "|" + want
									written[strings.ToLower(want)] = true
									continue
								}
							}
						}
					}
				}
			}
			q := url.Values{"b": {key(biz)}, "p": {key(it.ID)}}
			if p.Key != "p" {
				q.Set("c", p.Key[1:])
			}
			var buf bytes.Buffer
			s, err := src.Save(ctx, "photo", q, &buf, MaxPhoto)
			switch {
			case errors.Is(err, hub.ErrNothing):
				k.Photos[p.Key] = p.Ver + "|"
				continue
			case err != nil && Fatal(err):
				return kept, err
			case err != nil:
				res.Fail(g.Folder+" "+k.Dir, err)
				continue
			}
			file := p.File + photoExt(s.Type)
			ok, werr := write(filepath.Join(folder, file), buf.Bytes())
			switch {
			case werr != nil:
				res.Fail(g.Folder+" "+k.Dir, werr)
			case ok:
				if oldFile != "" && Ok(oldFile) && !strings.EqualFold(oldFile, file) {
					_ = os.Remove(filepath.Join(folder, oldFile))
				}
				k.Photos[p.Key] = p.Ver + "|" + file
				written[strings.ToLower(file)] = true
				res.Photos++
			default:
				res.Locked++
			}
			if asked++; asked%25 == 0 {
				if progress != nil {
					progress(fmt.Sprintf("%s: фото %d", g.Folder, asked))
				}
				if err := saveKept(manifest, kept); err != nil {
					return kept, err
				}
			}
		}
		// Фото, которого у проекта больше нет, — забываем; файл остаётся (вдруг он нужен владельцу).
		for k2 := range k.Photos {
			if !keys[k2] {
				delete(k.Photos, k2)
			}
		}
	}
	return kept, saveKept(manifest, kept)
}
