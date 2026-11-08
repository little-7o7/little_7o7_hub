package files

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Старые версии и шифрование (план pc-backup-2, решения 5 и 6; владелец 2026-11-08: «108. mojesh soxranit
// 1 mesyatsniy», «109. da mojesh. parol postavlenniy dlya backup»).
//
//   - Файл под паролем копий лежит как «<имя>.l7» (internal/fileseal); пароль копий не задан — открытым, как раньше.
//     Открытый файл прежней программы (1.2) при первой встрече шифруется на месте, время записи остаётся.
//   - Excel, записанный в прошлый день, перед заменой уходит в «Старые версии/<день>/<тот же путь>»: в течение дня
//     файл просто заменяется, в «Старых версиях» — по одному на день. Старше 30 дней — удаляет PruneOld.

// OldFolder — «Старые версии» в папке «Little Hub» диска.
const OldFolder = "Старые версии"

// Disk — куда и как писать: Root — папка «Little Hub» диска, Today — день по Ташкенту, Lock — ключ шифрования
// файлов (nil — пароль копий не задан, файлы открытые).
type Disk struct {
	Root  string
	Today string
	Lock  *fileseal.Lock
}

// tag — метка для версии бизнеса: включили шифрование или сменили пароль — бизнес проходим заново (шифруем, что лежит
// открытым).
func (d *Disk) tag() string {
	if d == nil || d.Lock == nil {
		return ""
	}
	return " l7:" + d.Lock.Tag()
}

// name — как файл path лежит на диске при этом ключе.
func (d *Disk) name(path string) string {
	if d != nil && d.Lock != nil {
		return path + fileseal.Ext
	}
	return path
}

// other — тот же файл в другом виде (открытый ↔ зашифрованный).
func other(target, path string) string {
	if target == path {
		return path + fileseal.Ext
	}
	return path
}

// find — где лежит файл path: зашифрованный, открытый или нигде ("").
func find(path string) string {
	if isFile(path + fileseal.Ext) {
		return path + fileseal.Ext
	}
	if isFile(path) {
		return path
	}
	return ""
}

// has — файл есть (в любом виде). Ключ есть, а файл открытый (записан до шифрования) — зашифровать на месте.
func (d *Disk) has(path string) bool {
	sealed, plain := isFile(path+fileseal.Ext), isFile(path)
	if d != nil && d.Lock != nil && plain {
		if sealed {
			_ = os.Remove(path) // зашифровали, а открытый не убрался (был открыт) — убрать сейчас
		} else {
			sealed = d.sealInPlace(path) == nil
		}
	}
	return sealed || plain
}

// sealInPlace — зашифровать открытый файл path рядом («<имя>.l7») с тем же временем записи и убрать открытый.
func (d *Disk) sealInPlace(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b, err := fileseal.Bytes(data, *d.Lock)
	if err != nil {
		return err
	}
	t, err := writeTemp(path+fileseal.Ext, b)
	if err != nil {
		return err
	}
	_ = os.Chtimes(t, st.ModTime(), st.ModTime())
	if err := os.Rename(t, path+fileseal.Ext); err != nil {
		_ = os.Remove(t)
		return err
	}
	return os.Remove(path)
}

// keepOld — прежний Excel прошлого дня — в «Старые версии/<день>/…». false — не вышло (файл открыт): не заменяем.
func (d *Disk) keepOld(path string) bool {
	if d == nil || d.Root == "" || d.Today == "" || !strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return true
	}
	have := find(path)
	if have == "" {
		return true
	}
	st, err := os.Stat(have)
	if err != nil {
		return true
	}
	day := st.ModTime().In(days.Tashkent).Format("2006-01-02")
	if day >= d.Today {
		return true
	}
	rel, err := filepath.Rel(d.Root, have)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return true
	}
	dst := filepath.Join(d.Root, OldFolder, day, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return true // «Старые версии» не записать — заменяем как раньше, свежий файл важнее
	}
	return os.Rename(have, dst) == nil
}

// place — готовый временный файл temp (уже в виде target) на место path: прежний Excel прошлого дня — в «Старые
// версии», файл в другом виде — убрать. false — файл открыт и не заменён.
func (d *Disk) place(temp, path, target string) bool {
	if !d.keepOld(path) {
		_ = os.Remove(temp)
		return false
	}
	if !put(temp, target) {
		return false
	}
	if o := other(target, path); isFile(o) {
		_ = os.Remove(o)
	}
	return true
}

// write — файл целиком (зашифрованным, если есть ключ): true — записан, false — открыт и не заменён.
func (d *Disk) write(path string, data []byte) (bool, error) {
	target := d.name(path)
	if target != path {
		b, err := fileseal.Bytes(data, *d.Lock)
		if err != nil {
			return false, err
		}
		data = b
	}
	t, err := writeTemp(target, data)
	if err != nil {
		return false, err
	}
	return d.place(t, path, target), nil
}

// save — файл проекта во временный рядом (шифруем на лету) и на место. ErrNothing — у проекта его нет.
func (d *Disk) save(ctx context.Context, src Source, op string, q url.Values, path string, max int64) (hub.Saved, bool, error) {
	return d.saveWith(path, func(w io.Writer) (hub.Saved, error) { return src.Save(ctx, op, q, w, max) })
}

// saveWith — то, что пишет get, во временный файл рядом (шифруем на лету) и на место path.
func (d *Disk) saveWith(path string, get func(w io.Writer) (hub.Saved, error)) (hub.Saved, bool, error) {
	target := d.name(path)
	t := target + tmp
	f, err := os.OpenFile(t, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return hub.Saved{}, false, err
	}
	var w io.Writer = f
	var sw io.WriteCloser
	if target != path {
		if sw, err = fileseal.NewWriter(f, *d.Lock); err != nil {
			_ = f.Close()
			_ = os.Remove(t)
			return hub.Saved{}, false, err
		}
		w = sw
	}
	s, err := get(w)
	if err == nil && sw != nil {
		err = sw.Close()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(t)
		return s, false, err
	}
	return s, d.place(t, path, target), nil
}

// rename — файл from (в любом виде) под имя to в том же виде.
func rename(from, to string) error {
	have := find(from)
	if have == "" {
		return os.ErrNotExist
	}
	if have != from {
		to += fileseal.Ext
	}
	return os.Rename(have, to)
}

// remove — файл path в любом виде.
func remove(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + fileseal.Ext)
}

// PruneOld — «Старые версии» старше keep дней: папки дней целиком.
func PruneOld(root string, now time.Time, keep int) int {
	dir := filepath.Join(root, OldFolder)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	since := days.Since(now, keep)
	n := 0
	for _, e := range entries {
		if e.IsDir() && days.ValidDay(e.Name()) && e.Name() < since && os.RemoveAll(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	if left, _ := os.ReadDir(dir); len(left) == 0 {
		_ = os.Remove(dir)
	}
	return n
}

// SealAll — зашифровать на месте открытые файлы, что программа клала в папки (переход на 1.3: Excel и файлы папок
// проходят has при раскладке, а это — для того, что раскладка не трогает: «Старые версии»). Файлы человека и служебные
// не трогаем: только .xlsx.
func SealAll(dir string, d *Disk) int {
	if d == nil || d.Lock == nil {
		return 0
	}
	n := 0
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.EqualFold(filepath.Ext(p), ".xlsx") {
			return nil
		}
		if d.sealInPlace(p) == nil {
			n++
		}
		return nil
	})
	return n
}
