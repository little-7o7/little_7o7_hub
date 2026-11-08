package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

// Файлы проектов (Excel, папки клиентов, поставщиков, мастеров, товаров с фото) и копии баз проектов: хаб спрашивает
// проект по его закрытому API и передаёт ответ как есть (little_7o7/src/lib/pc-projects.ts).

// MaxItems — ответ с файлами папок (до 25 папок в base64; проект держит его до 3 МБ, с запасом).
const MaxItems = 32 << 20

// APIError — хаб или проект ответил ошибкой (не связь): записываем и идём дальше, как старая Sarpo Business.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("ответ %d: %s", e.Status, e.Msg) }

// IsAPI — ошибка ответа (а не связи и не «компьютер отключён»).
func IsAPI(err error) bool {
	var a *APIError
	return errors.As(err, &a)
}

// PcFile — файл Excel бизнеса: key — что спросить, folder — папка внутри бизнеса («Excel» или ""), refresh —
// обновлять, когда бизнес изменился (текущий месяц, прибыль); остальные — только если файла нет.
type PcFile struct {
	Key     string `json:"key"`
	Folder  string `json:"folder"`
	Name    string `json:"name"`
	Refresh bool   `json:"refresh"`
}

// Business — бизнес проекта (магазин, гостиница): ver меняется при любой записи в нём и в новый день.
type Business struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Full bool   `json:"full"`
	// Root — у проекта один «бизнес» (Strassio): файлы прямо в папке проекта, без папки бизнеса.
	Root  bool     `json:"root,omitempty"`
	Ver   string   `json:"ver"`
	Files []PcFile `json:"files"`
}

// FileProject — проект с файлами. OK=false — проект не подключён или не ответил: его папку не трогаем.
type FileProject struct {
	Slug       string     `json:"slug"`
	Folder     string     `json:"folder"`
	Copy       bool       `json:"copy"`
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	Businesses []Business `json:"businesses"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9_-]{1,30}$`)
var opRe = regexp.MustCompile(`^[a-z]{1,20}$`)

// Projects — проекты с файлами и их бизнесы.
func (c *Client) Projects(ctx context.Context) ([]FileProject, error) {
	resp, err := c.do(ctx, "GET", "/api/pc/projects", nil, true)
	if err != nil {
		return nil, err
	}
	var r struct {
		Projects []FileProject `json:"projects"`
	}
	if err := readJSON(resp, &r); err != nil {
		return nil, err
	}
	return r.Projects, nil
}

func (c *Client) project(ctx context.Context, slug, op string, q url.Values) (*http.Response, error) {
	if !slugRe.MatchString(slug) || !opRe.MatchString(op) {
		return nil, fmt.Errorf("неверный адрес проекта: %s/%s", slug, op)
	}
	path := "/api/pc/p/" + slug + "/" + op
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.do(ctx, "GET", path, nil, true)
}

func apiError(resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	msg := e.Message
	if msg == "" {
		msg = e.Error
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	if resp.StatusCode == http.StatusConflict && e.Error == "no-password" {
		return ErrNoPassword
	}
	return &APIError{Status: resp.StatusCode, Msg: msg}
}

// ProjectJSON — ответ проекта в JSON (папки, файлы папок).
func (c *Client) ProjectJSON(ctx context.Context, slug, op string, q url.Values, out any) error {
	resp, err := c.project(ctx, slug, op, q)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return apiError(resp)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxItems+1))
	if err != nil {
		return fmt.Errorf("ответ не дошёл: %w", err)
	}
	if len(b) > MaxItems {
		return &APIError{Status: 502, Msg: "ответ больше 32 МБ"}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return &APIError{Status: 502, Msg: "ответ проекта не JSON"}
	}
	return nil
}

// Saved — что записали в файл: тип (Content-Type), байт, заголовки ответа.
type Saved struct {
	Type   string
	Bytes  int64
	Header http.Header
}

// ErrNothing — у проекта этого нет (фото нет — 404) или уже есть (копия та же — 204).
var ErrNothing = errors.New("нечего сохранять")

// ProjectSave — файл проекта (Excel, фото, копия базы) в w. 404 и 204 — ErrNothing. Больше max — ошибка.
func (c *Client) ProjectSave(ctx context.Context, slug, op string, q url.Values, w io.Writer, max int64) (Saved, error) {
	resp, err := c.project(ctx, slug, op, q)
	if err != nil {
		return Saved{}, err
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return Saved{}, ErrNothing
	}
	if resp.StatusCode != http.StatusOK {
		return Saved{}, apiError(resp)
	}
	defer resp.Body.Close()
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return Saved{}, fmt.Errorf("файл не дошёл: %w", err)
	}
	if n > max {
		return Saved{}, &APIError{Status: 502, Msg: "файл больше " + strconv.FormatInt(max>>20, 10) + " МБ"}
	}
	return Saved{Type: resp.Header.Get("Content-Type"), Bytes: n, Header: resp.Header}, nil
}
