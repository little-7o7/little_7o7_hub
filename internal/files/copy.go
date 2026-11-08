package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Копия базы проекта (Sarpo — ночная копия): хаб шифрует её паролем копий на лету, файл кладём в
// «<проект>/Копии базы/ГГГГ-ММ/<проект>-<день>.l7copy» — рядом с копиями хаба, тем же форматом.

var copyName = regexp.MustCompile(`^[a-z0-9_-]{1,30}-\d{4}-\d{2}-\d{2}\.sql\.gz$`)

// Copied — что сохранили: имя копии у проекта, день, размер, путь.
type Copied struct {
	Name  string
	Day   string
	Bytes int64
	Path  string
}

// ProjectCopy — последняя копия базы проекта в root (папка «Little Hub» диска). have — имя копии, что уже лежит по
// пути havePath: проект ответит «та же» (ErrNothing).
func ProjectCopy(ctx context.Context, src Source, p days.Project, root, have, havePath string) (Copied, error) {
	q := url.Values{}
	if have != "" && days.Exists(havePath) {
		q.Set("have", have)
	}
	dir := filepath.Join(root, p.Folder, "Копии базы")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Copied{}, err
	}
	t := filepath.Join(dir, "incoming"+tmp)
	f, err := os.OpenFile(t, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return Copied{}, err
	}
	s, err := src.Save(ctx, "backup", q, f, hub.MaxCopy)
	serr := f.Sync()
	cerr := f.Close()
	if err == nil {
		err = errors.Join(serr, cerr)
	}
	if err != nil {
		_ = os.Remove(t)
		return Copied{}, err
	}
	fail := func(e error) (Copied, error) { _ = os.Remove(t); return Copied{}, e }
	name, day := s.Header.Get("X-Copy-Name"), s.Header.Get("X-Copy-Day")
	want, _ := strconv.ParseInt(s.Header.Get("X-Copy-Bytes"), 10, 64)
	if !copyName.MatchString(name) || !days.ValidDay(day) {
		return fail(&hub.APIError{Status: 502, Msg: "копия без имени или дня"})
	}
	if want <= 0 || s.Bytes != want {
		return fail(&hub.APIError{Status: 502, Msg: fmt.Sprintf("копия пришла не целой: %d байт из %d", s.Bytes, want)})
	}
	head := make([]byte, copyfmt.HeadLen)
	if r, err := os.Open(t); err == nil {
		n, _ := io.ReadFull(r, head)
		_ = r.Close()
		head = head[:n]
	}
	if err := copyfmt.CheckHead(head, s.Bytes); err != nil {
		return fail(err)
	}
	rel, err := days.Rel(p, day)
	if err != nil {
		return fail(err)
	}
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail(err)
	}
	if err := os.Rename(t, path); err != nil {
		return fail(err)
	}
	return Copied{Name: name, Day: day, Bytes: s.Bytes, Path: path}, nil
}
