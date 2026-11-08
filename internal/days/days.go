// Package days — файл копии на каждый день (план pc-backup-1, решение 7; владелец 2026-11-08: «izmeniy predidushuyu.
// no soxtanit v 00.00 dlya etogo dnya. dlya zaftreshnogo drugoy fayl»). День — по Ташкенту (UTC+5, без перехода
// на летнее время). Пока день идёт, новая копия заменяет файл этого дня; после полуночи файл больше не меняется,
// следующая копия ложится в файл нового дня.
package days

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
