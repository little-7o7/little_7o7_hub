// Package disks — куда класть копии (план pc-backup-1, решение 8; владелец 2026-11-08: «ya dobavlyu postepenno hhd
// v kompyuterax, esli mesto ne xvatit»). Список дисков по порядку; копии пишутся в первый, где свободно не меньше
// 10% и не меньше 5 ГБ. Заполнился или отключён — следующий. На каждом диске — папка «Little Hub».
package disks

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	MinFreeShare = 0.10
	MinFreeBytes = 5_000_000_000
	// Folder — папка программы на каждом диске.
	Folder = "Little Hub"
)

// Space — свободно и всего на диске (байты).
type Space struct{ Free, Total uint64 }

// SpaceFunc — сколько места на диске по пути; подменяется в тестах.
type SpaceFunc func(path string) (Space, error)

// State — диск для окна программы и хаба.
type State struct {
	Path   string `json:"path"`
	Free   uint64 `json:"free"`
	Total  uint64 `json:"total"`
	Active bool   `json:"active"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// ErrNoDisk — ни на одном диске нет места или все недоступны.
var ErrNoDisk = errors.New("нет диска для копий: все заполнены или отключены — добавьте диск в окне Little Hub")

func enough(s Space) bool {
	return s.Total > 0 && s.Free >= MinFreeBytes && float64(s.Free) >= MinFreeShare*float64(s.Total)
}

// Check — состояние всех дисков и какой из них сейчас для копий (первый подходящий). Нет подходящего — ErrNoDisk,
// состояние всё равно возвращается (окно покажет, что не так).
func Check(paths []string, space SpaceFunc) ([]State, string, error) {
	out := make([]State, 0, len(paths))
	active := ""
	for _, p := range paths {
		st := State{Path: p}
		s, err := space(p)
		switch {
		case err != nil:
			st.Error = "диск недоступен"
		case !writable(p):
			st.Error = "нельзя записать"
			st.Free, st.Total = s.Free, s.Total
		default:
			st.Free, st.Total = s.Free, s.Total
			st.OK = enough(s)
			if !st.OK {
				st.Error = "мало места"
			}
		}
		if st.OK && active == "" {
			st.Active = true
			active = p
		}
		out = append(out, st)
	}
	if active == "" {
		return out, "", ErrNoDisk
	}
	return out, active, nil
}

// Root — папка программы на диске.
func Root(disk string) string { return filepath.Join(disk, Folder) }

// writable — можно ли создать папку программы и файл в ней (диск только для чтения, нет прав — нельзя).
func writable(disk string) bool {
	root := Root(disk)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(root, ".check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// Clean — путь диска без лишнего: «E:» → «E:\», хвостовые разделители убраны (кроме корня).
func Clean(p string) string {
	if len(p) == 2 && p[1] == ':' {
		p += string(filepath.Separator)
	}
	c := filepath.Clean(p)
	if len(c) == 2 && c[1] == ':' {
		c += string(filepath.Separator)
	}
	return c
}
