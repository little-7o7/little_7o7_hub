package files

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// «Фото базы» (план pc-backup-2, этап 3; владелец 2026-11-08: «v backupe toje doljno bit vsyo. v backup budetli
// fotografiii?»): копия базы проекта хранит у фото только путь в хранилище, сами фото — здесь:
//
//	<бизнес>/Фото базы/Чеки операций/<месяц>/…, Паспорта и документы гостей/<бронь>/…, Поломки в номерах/<месяц>/…,
//	                   QR поставщиков/…, Переводы валютчиков/<месяц>/…, Список фото.csv — где какое фото.
//
// Фото товаров и цветов сюда не входят: они уже лежат в папках «Товары» (каждое фото — один раз). Проект отдаёт список
// страницами (after — id) с подписанными ссылками в своё хранилище; программа качает только новые. Раз в день (и по
// «Сохранить сейчас») — весь список: переименованные (операцию поправили) переименовать, стёртые в хранилище отметить
// в списке — файл на диске остаётся.

const (
	MediaFolder = "Фото базы"
	MediaList   = "Список фото.csv"
	maxPages    = 1000
)

type mediaFile struct {
	ID      int     `json:"id"`
	Dir     string  `json:"dir"`
	Name    string  `json:"name"`
	URL     string  `json:"url"`
	Blob    string  `json:"blob"`
	What    string  `json:"what"`
	At      string  `json:"at"`
	Deleted *string `json:"deleted"`
}

type mediaOne struct {
	Path    string `json:"path"` // внутри «Фото базы», через /
	Blob    string `json:"blob"`
	What    string `json:"what,omitempty"`
	At      string `json:"at,omitempty"`
	Deleted string `json:"deleted,omitempty"` // удалено в базе (у проекта в корзине)
	Gone    string `json:"gone,omitempty"`    // день, когда его не стало в хранилище проекта
}

type mediaKept struct {
	Full  string               `json:"full"` // день последнего полного прохода
	Last  int                  `json:"last"` // до какого id всё скачано
	Files map[string]*mediaOne `json:"files"`
}

func loadMedia(path string) *mediaKept {
	m := &mediaKept{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, m)
	}
	if m.Files == nil {
		m.Files = map[string]*mediaOne{}
	}
	for id, one := range m.Files {
		if one == nil || !okRel(one.Path) {
			delete(m.Files, id)
		}
	}
	if m.Last < 0 {
		m.Last = 0
	}
	return m
}

// okRel — путь от проекта: до 4 частей через /, каждая годится для Windows и Mac.
func okRel(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if !Ok(p) {
			return false
		}
	}
	return true
}

func is404(err error) bool {
	var a *hub.APIError
	return errors.As(err, &a) && a.Status == 404
}

// Media — «Фото базы» бизнеса в dir. У проекта их нет (Strassio, старый Sarpo) — ничего. force — весь список заново.
func Media(ctx context.Context, src Source, biz int, dir string, d *Disk, force bool, res *Result, progress func(string)) error {
	root := filepath.Join(dir, MediaFolder)
	manifest := filepath.Join(root, Manifest)
	m := loadMedia(manifest)
	today := ""
	if d != nil {
		today = d.Today
	}
	full := force || today == "" || m.Full != today
	after := m.Last
	if full {
		after = 0
	}
	seen := map[string]bool{}
	changed := false
	top, failed := m.Last, 0
	got := 0
	for page := 0; page < maxPages; page++ {
		var a struct {
			Files []mediaFile `json:"files"`
			Next  *int        `json:"next"`
		}
		if err := src.JSON(ctx, "media", url.Values{"b": {strconv.Itoa(biz)}, "after": {strconv.Itoa(after)}}, &a); err != nil {
			if page == 0 && is404(err) {
				return nil
			}
			return err
		}
		if page == 0 && len(a.Files) > 0 {
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
		}
		for _, f := range a.Files {
			rel := f.Dir + "/" + f.Name
			if f.ID <= 0 || f.Dir == "" || !okRel(rel) || filepath.Ext(f.Name) == "" || f.URL == "" {
				continue
			}
			id := strconv.Itoa(f.ID)
			if seen[id] {
				continue
			}
			seen[id] = true
			top = max(top, f.ID)
			deleted := ""
			if f.Deleted != nil {
				deleted = *f.Deleted
			}
			path := filepath.Join(root, filepath.FromSlash(rel))
			one := m.Files[id]
			if one != nil && one.Blob == f.Blob && d.has(filepath.Join(root, filepath.FromSlash(one.Path))) {
				if one.Path != rel && find(path) == "" && os.MkdirAll(filepath.Dir(path), 0o755) == nil &&
					rename(filepath.Join(root, filepath.FromSlash(one.Path)), path) == nil {
					one.Path, changed = rel, true
				}
				if one.What != f.What || one.At != f.At || one.Deleted != deleted || one.Gone != "" {
					one.What, one.At, one.Deleted, one.Gone, changed = f.What, f.At, deleted, "", true
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				res.Fail(MediaFolder+" "+rel, err)
				failed = minFail(failed, f.ID)
				continue
			}
			link := f.URL
			_, ok, err := d.saveWith(path, func(w io.Writer) (hub.Saved, error) { return src.Fetch(ctx, link, w, MaxPhoto) })
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				res.Fail(MediaFolder+" "+rel, err)
				failed = minFail(failed, f.ID)
				continue
			}
			if !ok {
				res.Locked++
				failed = minFail(failed, f.ID)
				continue
			}
			if one != nil && one.Path != rel {
				remove(filepath.Join(root, filepath.FromSlash(one.Path))) // то же фото под старым именем
			}
			m.Files[id] = &mediaOne{Path: rel, Blob: f.Blob, What: f.What, At: f.At, Deleted: deleted}
			changed = true
			res.Media++
			if got++; got%25 == 0 {
				if progress != nil {
					progress(MediaFolder + " " + strconv.Itoa(got))
				}
				if err := saveMedia(manifest, m); err != nil {
					return err
				}
			}
		}
		if a.Next == nil || *a.Next <= after {
			break
		}
		after = *a.Next
	}
	// До какого id всё скачано: не скачалось — с него начнём в следующий раз (скачанные после него пропустим по has).
	m.Last = top
	if failed > 0 {
		m.Last = failed - 1
	}
	// Весь список пришёл пустым при прежних фото — похоже на сбой: ничего не отмечаем.
	if full && len(seen) > 0 {
		for id, one := range m.Files {
			if !seen[id] && one.Gone == "" {
				one.Gone, changed = today, true
			}
		}
		m.Full = today
	}
	if len(m.Files) == 0 {
		return nil
	}
	list := filepath.Join(root, MediaList)
	if changed || !d.has(list) {
		if ok, err := d.write(list, mediaCSV(m)); err != nil {
			res.Fail(MediaFolder+" "+MediaList, err)
		} else if !ok {
			res.Locked++
		}
	}
	return saveMedia(manifest, m)
}

func minFail(failed, id int) int {
	if failed == 0 || id < failed {
		return id
	}
	return failed
}

func saveMedia(path string, m *mediaKept) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeHidden(path, b)
}

// when — время от проекта по Ташкенту «2026-11-08 14:05»; непонятное — как есть.
func when(s string) string {
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05.999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(days.Tashkent).Format("2006-01-02 15:04")
		}
	}
	return s
}

// mediaCSV — «Список фото.csv» для Excel: точка с запятой, UTF-8 с BOM, строки по пути файла.
func mediaCSV(m *mediaKept) []byte {
	rows := make([]*mediaOne, 0, len(m.Files))
	for _, one := range m.Files {
		rows = append(rows, one)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	w.UseCRLF = true
	_ = w.Write([]string{"Файл", "Что это", "Добавлено", "Удалено в базе", "Стёрто в хранилище", "Путь в хранилище"})
	for _, r := range rows {
		_ = w.Write([]string{filepath.FromSlash(r.Path), r.What, when(r.At), when(r.Deleted), r.Gone, r.Blob})
	}
	w.Flush()
	return buf.Bytes()
}
