package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/files"
	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
)

// Окно «Файлы» (план pc-backup-2, решение 7; пункты 85 и 109): файлы проектов под паролем копий в Проводнике не
// открыть, поэтому их открывают здесь. Пароль копий спрашиваем один раз и помним 15 минут — только в памяти. Файл
// расшифровывается во временную папку программы (не в «Little Hub») и открывается в Excel или просмотре фото; через час
// и при выходе временные файлы стираются. «Сохранить открытыми» — расшифровать папку целиком рядом с «Little Hub»
// (восстановление, перенос): тогда файлы лежат без пароля, и окно об этом предупреждает.

const (
	PassKeep = 15 * time.Minute
	OpenKeep = time.Hour
	// ExportFolder — куда «Сохранить открытыми»: рядом с «Little Hub» на том же диске.
	ExportFolder = "Little Hub — открытые"
)

var (
	ErrNeedPassword = errors.New("нужен пароль копий")
	errBadPath      = errors.New("неверный путь")
)

// Entry — файл или папка в окне: имя — как у человека (без «.l7»).
type Entry struct {
	Name   string `json:"name"`
	Dir    bool   `json:"dir"`
	Size   int64  `json:"size,omitempty"`
	At     string `json:"at,omitempty"`
	Sealed bool   `json:"sealed,omitempty"`
	Copy   bool   `json:"copy,omitempty"`
}

// Folder — папка в окне «Файлы»: Path — от «Little Hub», через «/».
type Folder struct {
	Path     string  `json:"path"`
	Root     string  `json:"root"`
	Entries  []Entry `json:"entries"`
	Unlocked bool    `json:"unlocked"`
	Sealed   bool    `json:"sealed"`
}

// hidden — служебные файлы программы в окне не показываем.
func hidden(name string) bool {
	return name == files.Manifest || name == files.BizVer || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~lh-") ||
		strings.HasSuffix(name, ".lh-tmp") || strings.HasSuffix(name, ".tmp")
}

// place — полный путь по пути из окна: только внутри «Little Hub» активного диска, без «..», дисков и ссылок наружу.
func (a *App) place(rel string) (root, full string, err error) {
	root, err = a.FolderOf("")
	if err != nil {
		return "", "", err
	}
	rel = strings.Trim(rel, "/")
	if strings.ContainsAny(rel, "\\:\x00") {
		return "", "", errBadPath
	}
	for _, seg := range strings.Split(rel, "/") {
		if rel != "" && (seg == "" || seg == "." || seg == ".." || hidden(seg)) {
			return "", "", errBadPath
		}
	}
	full = filepath.Join(root, filepath.FromSlash(rel))
	realRoot, err1 := filepath.EvalSymlinks(root)
	realFull, err2 := filepath.EvalSymlinks(full)
	if err1 != nil {
		return "", "", err1
	}
	if err2 == nil {
		if r, err := filepath.Rel(realRoot, realFull); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", "", errBadPath
		}
	}
	return root, full, nil
}

// Browse — что лежит в папке rel.
func (a *App) Browse(rel string) (Folder, error) {
	root, full, err := a.place(rel)
	if err != nil {
		return Folder{}, err
	}
	list, err := os.ReadDir(full)
	if err != nil {
		return Folder{}, err
	}
	f := Folder{Path: strings.Trim(rel, "/"), Root: root, Entries: []Entry{}, Unlocked: a.keysNow() != nil, Sealed: a.Cfg.Get().Lock != nil}
	for _, e := range list {
		name := e.Name()
		if hidden(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		en := Entry{Name: name, Dir: e.IsDir(), At: info.ModTime().In(days.Tashkent).Format("2006-01-02 15:04")}
		if !e.IsDir() {
			if !info.Mode().IsRegular() {
				continue
			}
			en.Size = info.Size()
			switch {
			case strings.HasSuffix(name, fileseal.Ext):
				en.Name, en.Sealed = strings.TrimSuffix(name, fileseal.Ext), true
			case strings.HasSuffix(name, ".l7copy"):
				en.Copy, en.Sealed = true, true
			}
		}
		f.Entries = append(f.Entries, en)
	}
	slices.SortFunc(f.Entries, func(x, y Entry) int {
		if x.Dir != y.Dir {
			if x.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	return f, nil
}

// Unlock — пароль копий для окна «Файлы» (15 минут, только в памяти). Есть ключ от хаба — сверяем сразу.
func (a *App) Unlock(password string) error {
	if password == "" {
		return ErrNeedPassword
	}
	keys := &fileseal.Keys{Password: password}
	if l := a.Cfg.Get().Lock; l != nil {
		if _, err := keys.For(*l); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.keys, a.keysAt = keys, a.now()
	a.mu.Unlock()
	return nil
}

// Forget — забыть пароль сейчас («Закрыть»).
func (a *App) Forget() {
	a.mu.Lock()
	a.keys = nil
	a.mu.Unlock()
}

func (a *App) keysNow() *fileseal.Keys {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.keys != nil && a.now().Sub(a.keysAt) > PassKeep {
		a.keys = nil
	}
	return a.keys
}

func (a *App) openDir() (string, error) {
	if a.OpenDir != "" {
		return a.OpenDir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Little Hub", "open"), nil
}

// tempFor — своя временная папка для одного открытия: «<время>-<случайное>».
func (a *App) tempFor() (string, error) {
	base, err := a.openDir()
	if err != nil {
		return "", err
	}
	r := make([]byte, 6)
	_, _ = rand.Read(r)
	dir := filepath.Join(base, fmt.Sprintf("%d-%s", a.now().Unix(), hex.EncodeToString(r)))
	return dir, os.MkdirAll(dir, 0o700)
}

// Opened — что открыть: файл (Path) или папку с ним (Reveal — копия базы: SQL открывать нечем, показываем файл).
type Opened struct {
	Path   string `json:"path"`
	Reveal bool   `json:"reveal,omitempty"`
}

// OpenFile — файл rel для открытия: открытый — как есть, под паролем — расшифровать во временную папку.
func (a *App) OpenFile(rel string) (Opened, error) {
	_, full, err := a.place(rel)
	if err != nil {
		return Opened{}, err
	}
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		return Opened{Path: full}, nil
	}
	sealed := full + fileseal.Ext
	isCopy := strings.HasSuffix(full, ".l7copy")
	if !isCopy && !isRegular(sealed) {
		if !isRegular(full) {
			return Opened{}, os.ErrNotExist
		}
		// Установщик из «Программ» окно не запускает — показывает в папке: запустить его — решение человека.
		if r := filepath.ToSlash(rel); r == files.ProgramsFolder || strings.HasPrefix(r, files.ProgramsFolder+"/") {
			return Opened{Path: full, Reveal: true}, nil
		}
		return Opened{Path: full, Reveal: !viewable(full)}, nil // открытый файл (пароль копий не задан) — прямо с диска
	}
	keys := a.keysNow()
	if keys == nil {
		return Opened{}, ErrNeedPassword
	}
	dir, err := a.tempFor()
	if err != nil {
		return Opened{}, err
	}
	if isCopy {
		out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(full), ".l7copy")+".sql")
		if err := openCopy(full, out, keys); err != nil {
			_ = os.RemoveAll(dir)
			return Opened{}, err
		}
		return Opened{Path: out, Reveal: true}, nil
	}
	out := filepath.Join(dir, filepath.Base(full))
	if err := openSealed(sealed, out, keys); err != nil {
		_ = os.RemoveAll(dir)
		return Opened{}, err
	}
	return Opened{Path: out, Reveal: !viewable(out)}, nil
}

// viewable — файл с таким расширением окно открывает само (Excel, PDF, фото, текст). Другое (.exe, .bat, .lnk…) —
// только показывает в папке: имя файла пришло от проекта, запускать его — решение человека.
func viewable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".xls", ".csv", ".pdf", ".txt", ".docx", ".doc", ".odt", ".ods",
		".jpg", ".jpeg", ".png", ".webp", ".gif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".mp4", ".mov":
		return true
	}
	return false
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// openSealed — файл L7-F-1 расшифровать в out (кусками: фото и большие Excel в памяти целиком не держим).
func openSealed(src, out string, keys *fileseal.Keys) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	r, err := fileseal.NewReader(in, keys)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out)
	}
	return err
}

// openCopy — копия базы (L7-DB-1) → SQL.
func openCopy(src, out string, keys *fileseal.Keys) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	plain, err := copyfmt.Open(b, keys.Password)
	if err != nil {
		return err
	}
	return os.WriteFile(out, plain, 0o600)
}

// Export — «Сохранить открытыми»: папку или файл rel расшифровать в «Little Hub — открытые/<время>/<rel>» на том же
// диске. Ответ — папка, куда легло, и сколько файлов.
func (a *App) Export(rel string) (string, int, error) {
	root, full, err := a.place(rel)
	if err != nil {
		return "", 0, err
	}
	keys := a.keysNow()
	if keys == nil && a.Cfg.Get().Lock != nil {
		return "", 0, ErrNeedPassword
	}
	dest := filepath.Join(filepath.Dir(root), ExportFolder, a.now().In(days.Tashkent).Format("2006-01-02 15-04"))
	n, failed := 0, 0
	one := func(src, rel string) {
		name := filepath.Base(src)
		dst := filepath.Join(dest, filepath.Dir(rel))
		if os.MkdirAll(dst, 0o755) != nil {
			failed++
			return
		}
		var err error
		switch {
		case strings.HasSuffix(name, fileseal.Ext) && keys != nil:
			err = openSealed(src, filepath.Join(dst, strings.TrimSuffix(name, fileseal.Ext)), keys)
		case strings.HasSuffix(name, ".l7copy") && keys != nil:
			err = openCopy(src, filepath.Join(dst, strings.TrimSuffix(name, ".l7copy")+".sql"), keys)
		case strings.HasSuffix(name, fileseal.Ext) || strings.HasSuffix(name, ".l7copy"):
			return // без пароля не открыть — пропускаем
		default:
			err = copyFile(src, filepath.Join(dst, name))
		}
		if err != nil {
			failed++
			return
		}
		n++
	}
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		err = filepath.WalkDir(full, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if hidden(e.Name()) && p != full {
				if e.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if e.IsDir() || !e.Type().IsRegular() {
				return nil
			}
			r, err := filepath.Rel(root, p)
			if err == nil {
				one(p, r)
			}
			return nil
		})
	} else {
		src := full
		if isRegular(full + fileseal.Ext) {
			src = full + fileseal.Ext
		}
		r, _ := filepath.Rel(root, src)
		one(src, r)
	}
	if n == 0 && failed > 0 {
		return "", 0, fmt.Errorf("не сохранилось ни одного файла (%d ошибок)", failed)
	}
	note := ""
	if failed > 0 {
		note = fmt.Sprintf(", не вышло: %d", failed)
	}
	a.Logf(failed > 0, "Сохранено открытыми (без пароля): %d файлов в %s%s", n, dest, note)
	return dest, n, err
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// CleanOpen — стереть расшифрованные временные файлы: старше часа (all — все: запуск и выход).
func (a *App) CleanOpen(all bool) {
	base, err := a.openDir()
	if err != nil {
		return
	}
	list, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range list {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if all || a.now().Sub(info.ModTime()) > OpenKeep {
			_ = os.RemoveAll(filepath.Join(base, e.Name())) // открыт в Excel (Windows) — сотрём в следующий раз
		}
	}
}
