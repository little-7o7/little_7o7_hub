// Package days — файл копии на каждый день (план pc-backup-1, решение 7; владелец 2026-11-08: «izmeniy predidushuyu.
// no soxtanit v 00.00 dlya etogo dnya. dlya zaftreshnogo drugoy fayl»). День — по Ташкенту (UTC+5, без перехода
// на летнее время). Пока день идёт, новая копия заменяет файл этого дня; после полуночи файл больше не меняется,
// следующая копия ложится в файл нового дня.
package days

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Tashkent — UTC+5 круглый год. Своя зона, а не tzdata: на Windows базы зон может не быть.
var Tashkent = time.FixedZone("Asia/Tashkent", 5*3600)

// Today — день по Ташкенту: YYYY-MM-DD.
func Today(now time.Time) string { return now.In(Tashkent).Format("2006-01-02") }

var dayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var slugRe = regexp.MustCompile(`^[a-z0-9_-]{1,30}$`)

// ValidDay — настоящая дата YYYY-MM-DD.
func ValidDay(day string) bool {
	if !dayRe.MatchString(day) {
		return false
	}
	t, err := time.Parse("2006-01-02", day)
	return err == nil && t.Format("2006-01-02") == day
}

// Near — день копии, который прислал хаб, рядом с сегодняшним по часам компьютера (вчера, сегодня или завтра):
// часы компьютера могут спешить или отставать, но копия «за прошлый месяц» — ошибка.
func Near(day string, now time.Time) bool {
	if !ValidDay(day) {
		return false
	}
	for _, d := range []int{-1, 0, 1} {
		if Today(now.AddDate(0, 0, d)) == day {
			return true
		}
	}
	return false
}

// Project — что копируем: slug для имени файла и папка проекта по-человечески.
type Project struct {
	Slug   string // hub, sarpo, strassio
	Folder string // «Хаб», «Sarpo Business»
	Ext    string // l7copy
}

// Rel — путь файла дня внутри папки «Little Hub» диска: «Хаб/Копии базы/2026-11/hub-2026-11-08.l7copy».
func Rel(p Project, day string) (string, error) {
	if !slugRe.MatchString(p.Slug) || !ValidDay(day) || p.Folder == "" || filepath.Base(p.Folder) != p.Folder {
		return "", fmt.Errorf("неверное имя копии: %q %q", p.Slug, day)
	}
	return filepath.Join(p.Folder, "Копии базы", day[:7], fmt.Sprintf("%s-%s.%s", p.Slug, day, p.Ext)), nil
}

// ErrSame — файл дня уже такой же: писать нечего.
var ErrSame = errors.New("файл дня не изменился")

// Put — записать копию в файл дня атомарно: временный файл рядом, проверка SHA-256 записанного, замена.
// Тот же файл (по SHA-256) — ErrSame, диск не трогаем.
func Put(path string, data []byte, wantSha string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if wantSha != "" && got != wantSha {
		return fmt.Errorf("копия пришла не целой: SHA-256 %s, ждали %s", got, wantSha)
	}
	if old, err := fileSha(path); err == nil && old == got {
		return ErrSame
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if back, err := fileSha(tmp); err != nil || back != got {
		_ = os.Remove(tmp)
		return fmt.Errorf("диск записал копию с ошибкой")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	Record(path, got)
	return nil
}

func fileSha(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Exists — есть ли файл дня.
func Exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// Dated — файл дня в папке копий проекта и его день.
type Dated struct {
	Day  string
	Path string
}

// List — файлы дней проекта на диске («Little Hub» диска root), старые первыми.
func List(root string, p Project) []Dated {
	base := filepath.Join(root, p.Folder, "Копии базы")
	months, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(p.Slug) + `-(\d{4}-\d{2}-\d{2})\.` + regexp.QuoteMeta(p.Ext) + `$`)
	var out []Dated
	for _, m := range months {
		if !m.IsDir() || !monthRe.MatchString(m.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(base, m.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if g := re.FindStringSubmatch(f.Name()); g != nil && f.Type().IsRegular() && ValidDay(g[1]) {
				out = append(out, Dated{Day: g[1], Path: filepath.Join(base, m.Name(), f.Name())})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

var monthRe = regexp.MustCompile(`^\d{4}-\d{2}$`)

// Since — день на n дней раньше сегодняшнего (по Ташкенту).
func Since(now time.Time, n int) string { return Today(now.AddDate(0, 0, -n)) }

// Prune — файлы дней старше keep дней (владелец 2026-11-08: «1 mesyats») удалить, пустые папки месяцев — тоже.
// Ответ — сколько удалено.
func Prune(root string, p Project, now time.Time, keep int) int {
	oldest := Since(now, keep)
	n := 0
	dirs := map[string]bool{}
	for _, d := range List(root, p) {
		if d.Day >= oldest {
			continue
		}
		if os.Remove(d.Path) == nil {
			n++
			dirs[filepath.Dir(d.Path)] = true
		}
	}
	for dir := range dirs {
		if left, err := os.ReadDir(dir); err == nil && len(left) == 0 {
			_ = os.Remove(dir)
		}
	}
	return n
}

// ---------- Проверка копий раз в неделю (пункт 86) ----------

// ShaFile — SHA-256 записанных файлов дней: «<проект>/Копии базы/.littlehub-sha.json», ключ — «ГГГГ-ММ/имя».
const ShaFile = ".littlehub-sha.json"

func shaPlace(path string) (manifest, key string) {
	month := filepath.Dir(path)
	return filepath.Join(filepath.Dir(month), ShaFile), filepath.Base(month) + "/" + filepath.Base(path)
}

func readShas(manifest string) map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(manifest); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func writeShas(manifest string, m map[string]string) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	tmp := manifest + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil && os.Rename(tmp, manifest) != nil {
		_ = os.Remove(tmp)
	}
}

// Record — запомнить SHA-256 файла дня, который только что записали.
func Record(path, sha string) {
	manifest, key := shaPlace(path)
	m := readShas(manifest)
	if m[key] == sha {
		return
	}
	m[key] = sha
	writeShas(manifest, m)
}

// Verify — перечитать файлы дней проекта и сверить с записанным SHA-256; check — похож ли файл на копию (заголовок и
// размер, пароль не нужен). Испорчен — удалить: докачка возьмёт его заново (хаб и Sarpo держат 30 дней). Записи нет
// (копия от прежней программы) — запомнить. Записи удалённых файлов — забыть. Ответ — сколько проверено и испорченные.
func Verify(root string, p Project, headLen int, check func(head []byte, size int64) error) (int, []string) {
	list := List(root, p)
	manifest := filepath.Join(root, p.Folder, "Копии базы", ShaFile)
	m := readShas(manifest)
	keep := map[string]string{}
	n := 0
	var bad []string
	for _, d := range list {
		_, key := shaPlace(d.Path)
		sum, err := fileSha(d.Path)
		if err != nil {
			continue
		}
		head := make([]byte, headLen)
		size := int64(0)
		if f, err := os.Open(d.Path); err == nil {
			k, _ := io.ReadFull(f, head)
			head = head[:k]
			if st, err := f.Stat(); err == nil {
				size = st.Size()
			}
			_ = f.Close()
		}
		n++
		if (m[key] != "" && m[key] != sum) || (check != nil && check(head, size) != nil) {
			if os.Remove(d.Path) == nil {
				bad = append(bad, d.Day)
			}
			continue
		}
		keep[key] = sum
	}
	if len(list) > 0 || len(m) > 0 {
		writeShas(manifest, keep)
	}
	return n, bad
}
