// Package config — настройки программы в config.json (без секретов: ключ компьютера — в пакете secret).
// Windows — %APPDATA%\Little Hub, Mac — ~/Library/Application Support/Little Hub.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
)

// Copy — последняя копия проекта на этом компьютере.
type Copy struct {
	Day     string `json:"day"`
	At      string `json:"at"`
	Bytes   int64  `json:"bytes"`
	Version string `json:"version,omitempty"`
	Disk    string `json:"disk,omitempty"`
}

// ProjectFiles — файлы проекта на этом компьютере в последний раз (Excel, папки, фото).
type ProjectFiles struct {
	Businesses int      `json:"businesses"`
	Files      int      `json:"files"`
	Items      int      `json:"items"`
	Photos     int      `json:"photos"`
	Media      int      `json:"media,omitempty"`
	Locked     int      `json:"locked"`
	Errors     []string `json:"errors"`
}

// Files — когда последний раз раскладывали файлы проектов и что вышло.
type Files struct {
	At       string                  `json:"at"`
	Disk     string                  `json:"disk,omitempty"`
	Projects map[string]ProjectFiles `json:"projects"`
}

type Config struct {
	Hub      string          `json:"hub"`            // https://little-7o7.vercel.app
	Flag     string          `json:"flag,omitempty"` // /api/pc/flag/<ключ>
	DeviceID int             `json:"deviceId,omitempty"`
	Name     string          `json:"name,omitempty"`
	Disks    []string        `json:"disks"`
	Copies   map[string]Copy `json:"copies,omitempty"`
	Lang     string          `json:"lang,omitempty"`
	// RatesAt — когда курсы последний раз ушли в хаб (RFC 3339).
	RatesAt string `json:"ratesAt,omitempty"`
	// Files — файлы проектов (Excel, папки клиентов, поставщиков, мастеров, товаров с фото).
	Files *Files `json:"files,omitempty"`
	// Shortcuts — ярлыки в «Пуске» и на рабочем столе уже сделаны (делаем один раз: убранный ярлык не возвращаем).
	Shortcuts bool `json:"shortcuts,omitempty"`
	// Lock — открытый ключ шифрования файлов из пароля копий (от хаба; не секрет). nil — файлы пишутся открытыми.
	Lock *fileseal.Lock `json:"lock,omitempty"`
	// CheckedAt — когда последний раз проверяли копии баз (раз в неделю, RFC 3339).
	CheckedAt string `json:"checkedAt,omitempty"`
	// ProgramsDay — день (по Ташкенту), когда «Программы» разложены без ошибок: раз в день.
	ProgramsDay string `json:"programsDay,omitempty"`
	// Today — что записано за сегодня (по Ташкенту): для «Что сохранилось сегодня.txt».
	Today *Today `json:"today,omitempty"`
}

// Today — записано за день по проектам (все раскладки дня вместе) и в «Программы».
type Today struct {
	Day      string              `json:"day"`
	Projects map[string]DayCount `json:"projects"`
	Programs int                 `json:"programs,omitempty"`
}

// DayCount — файлов, папок, фото и «Фото базы» записано за день.
type DayCount struct {
	Files  int `json:"files,omitempty"`
	Items  int `json:"items,omitempty"`
	Photos int `json:"photos,omitempty"`
	Media  int `json:"media,omitempty"`
}

// Store — config.json с замком: окно и фоновая работа меняют его из разных горутин.
type Store struct {
	mu   sync.Mutex
	path string
	c    Config
}

// Dir — папка настроек программы.
func Dir() (string, error) {
	if d := os.Getenv("LITTLEHUB_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Little Hub"), nil
}

// Load — прочитать; файла нет — пустые настройки.
func Load(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "config.json")}
	b, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &s.c); err != nil {
			return nil, err
		}
	}
	if s.c.Copies == nil {
		s.c.Copies = map[string]Copy{}
	}
	return s, nil
}

// Get — копия настроек.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.c
	c.Disks = append([]string(nil), s.c.Disks...)
	c.Copies = make(map[string]Copy, len(s.c.Copies))
	for k, v := range s.c.Copies {
		c.Copies[k] = v
	}
	return c
}

// Update — поменять и сразу записать (атомарно: временный файл и замена).
func (s *Store) Update(f func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.c
	next.Disks = append([]string(nil), s.c.Disks...)
	next.Copies = make(map[string]Copy, len(s.c.Copies))
	for k, v := range s.c.Copies {
		next.Copies[k] = v
	}
	f(&next)
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.c = next
	return nil
}
