package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// ProgramsFolder — «Little Hub/Программы» (план pc-backup-2, этап 4, пункты 63, 79–81, 87): установщики Little Hub,
// плагина Strassio и Sarpo Business всех версий и тексты их выпусков. Поставить программу на новый компьютер или
// вернуть старую версию можно без интернета. Файлы открытые (не под паролем копий): установщик запускают раньше,
// чем есть Little Hub, и в нём нет данных проектов.
const ProgramsFolder = "Программы"

// ProgramSource — откуда берутся программы (хаб; в проверках — подставной).
type ProgramSource interface {
	Programs(ctx context.Context) ([]hub.Program, error)
	ProgramFile(ctx context.Context, p hub.Program, w io.Writer) (int64, error)
}

// ProgramsResult — сколько файлов записано за раз, сколько уже было, что не вышло.
type ProgramsResult struct {
	Files  int
	Have   int
	Errors []string
}

var (
	hex64    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	knownExt = regexp.MustCompile(`(?i)\.(exe|msi|zip|7z|dmg|pkg|txt|md|pdf)$`)
)

// programPath — путь файла в «Программах»: каждая часть папки и имя — годные имена, не глубже трёх папок.
func programPath(root string, p hub.Program) (string, bool) {
	parts := strings.Split(p.Dir, "/")
	if p.Dir == "" || len(parts) > 3 || !Ok(p.Name) {
		return "", false
	}
	for _, x := range parts {
		if !Ok(x) {
			return "", false
		}
	}
	return filepath.Join(append([]string{root, ProgramsFolder}, append(parts, p.Name)...)...), true
}

// Programs — разложить «Программы»: установщик — один раз (есть файл того же размера — не качаем), с проверкой
// размера и SHA-256; текст выпуска — если поправили. Сбой одного файла — в Errors, остальные качаются.
func Programs(ctx context.Context, src ProgramSource, root string) (ProgramsResult, error) {
	res := ProgramsResult{Errors: []string{}}
	list, err := src.Programs(ctx)
	if errors.Is(err, hub.ErrNothing) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	seen := map[string]bool{}
	for _, p := range list {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		path, ok := programPath(root, p)
		key := strings.ToLower(path)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			res.Errors = append(res.Errors, p.Name+": "+err.Error())
			continue
		}
		if p.URL == "" {
			wrote, err := programText(path, p.Text)
			switch {
			case err != nil:
				res.Errors = append(res.Errors, p.Name+": "+err.Error())
			case wrote:
				res.Files++
			default:
				res.Have++
			}
			continue
		}
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() && (p.Size <= 0 || st.Size() == p.Size) {
			res.Have++
			continue
		}
		if err := programFile(ctx, src, p, path); err != nil {
			if Fatal(err) {
				return res, err // нет связи — остальное в следующий раз
			}
			res.Errors = append(res.Errors, p.Name+": "+err.Error())
			continue
		}
		res.Files++
	}
	return res, nil
}

// programText — «Что нового <версия>.txt» (UTF-8 с BOM — Блокнот покажет кириллицу). Тот же текст — не пишем.
func programText(path, text string) (bool, error) {
	if strings.TrimSpace(text) == "" {
		return false, nil
	}
	t := strings.ReplaceAll(text, "\r\n", "\n")
	if runtime.GOOS == "windows" {
		t = strings.ReplaceAll(t, "\n", "\r\n")
	}
	b := append([]byte("\xef\xbb\xbf"), t...)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return false, nil
	}
	tp, err := writeTemp(path, b)
	if err != nil {
		return false, err
	}
	if !put(tp, path) {
		return false, errors.New("файл открыт, не заменился")
	}
	return true, nil
}

// programFile — установщик во временный файл рядом, сверка размера и SHA-256, потом на место. Не сошлось — файла нет.
func programFile(ctx context.Context, src ProgramSource, p hub.Program, path string) error {
	t := path + tmp
	f, err := os.OpenFile(t, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, gerr := src.ProgramFile(ctx, p, io.MultiWriter(f, h))
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(gerr, serr, cerr); err != nil {
		_ = os.Remove(t)
		return err
	}
	want := strings.ToLower(p.Sha256)
	if (p.Size > 0 && n != p.Size) || (hex64.MatchString(want) && hex.EncodeToString(h.Sum(nil)) != want) {
		_ = os.Remove(t)
		return &hub.APIError{Status: 502, Msg: "пришёл не целым (размер или SHA-256 не сходятся)"}
	}
	// Файл Mac без расширения — программа: можно запустить из Терминала.
	if runtime.GOOS != "windows" && !knownExt.MatchString(p.Name) {
		_ = os.Chmod(t, 0o755)
	}
	if err := os.Rename(t, path); err != nil {
		_ = os.Remove(t)
		return err
	}
	return nil
}
