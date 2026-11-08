// Package hub — запросы программы к хабу little_7o7 (/api/pc/*, little_7o7/src/app/api/pc). Вход — ключ компьютера
// в Authorization: Bearer. Хаб только по https (http — только 127.0.0.1 для тестов).
package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

var (
	// ErrRevoked — хаб не знает этот ключ: компьютер отключили в Настройках → Компьютеры.
	ErrRevoked = errors.New("компьютер отключён в хабе: подключите его заново кодом из Настроек → Компьютеры")
	// ErrNoPassword — в хабе не задан пароль копий.
	ErrNoPassword = errors.New("в хабе не задан пароль копий: Настройки → Компьютеры → «Пароль копий»")
	ErrBadCode    = errors.New("код неверный, уже использован или устарел — возьмите новый в хабе")
)

const (
	maxJSON     = 4 << 20
	MaxCopy     = 512 << 20
	MaxDownload = 200 << 20
)

type Client struct {
	Base    string
	Token   string
	Version string
	HTTP    *http.Client
}

// CheckBase — адрес хаба: https без пути и логина (http — только 127.0.0.1).
func CheckBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", fmt.Errorf("неверный адрес хаба: %q", raw)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && strings.HasPrefix(u.Host, "127.0.0.1:")) {
		return "", fmt.Errorf("адрес хаба — только https: %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func (c *Client) client() *http.Client {
	h := http.Client{Timeout: 5 * time.Minute}
	if c.HTTP != nil {
		h = *c.HTTP
	}
	h.CheckRedirect = noKeyElsewhere
	return &h
}

// noKeyElsewhere — переадресация (файл программы — на GitHub): ключ компьютера уходит только хабу, а не на другой
// адрес (даже на тот же компьютер с другим портом), и не дальше трёх переадресаций.
func noKeyElsewhere(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("слишком много переадресаций")
	}
	if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		req.Header.Del("Authorization")
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, auth bool) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("LittleHub/%s (%s; %s)", c.Version, runtime.GOOS, runtime.GOARCH))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if c.Token == "" {
			return nil, ErrRevoked
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("хаб не ответил: %w", err)
	}
	if auth && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrRevoked
	}
	return resp, nil
}

func readJSON(resp *http.Response, out any) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxJSON))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
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
		return fmt.Errorf("хаб ответил %d: %s", resp.StatusCode, msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// Paired — ответ хаба на код подключения.
type Paired struct {
	Token string `json:"token"`
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Flag  string `json:"flag"`
}

// Pair — обменять код из Настроек → Компьютеры на ключ компьютера.
func (c *Client) Pair(ctx context.Context, code, name string) (Paired, error) {
	var p Paired
	resp, err := c.do(ctx, "POST", "/api/pc/pair", map[string]string{"code": code, "name": name, "os": runtime.GOOS, "arch": runtime.GOARCH, "version": c.Version}, false)
	if err != nil {
		return p, err
	}
	if resp.StatusCode == http.StatusBadRequest {
		resp.Body.Close()
		return p, ErrBadCode
	}
	if err := readJSON(resp, &p); err != nil {
		return p, err
	}
	if len(p.Token) != 43 || !strings.HasPrefix(p.Flag, "/api/pc/flag/") {
		return p, errors.New("хаб ответил странно на код подключения")
	}
	return p, nil
}

// Flag — отпечаток данных хаба (из кэша CDN; меняется после каждой записи в хаб).
func (c *Client) Flag(ctx context.Context, path string) (string, error) {
	if !strings.HasPrefix(path, "/api/pc/flag/") {
		return "", errors.New("нет адреса флага: подключите компьютер заново")
	}
	resp, err := c.do(ctx, "GET", path, nil, false)
	if err != nil {
		return "", err
	}
	var f struct {
		V string `json:"v"`
	}
	if err := readJSON(resp, &f); err != nil {
		return "", err
	}
	return f.V, nil
}

// Copy — копия базы хаба (зашифрована паролем копий) и что о ней сказал хаб.
type Copy struct {
	Data    []byte
	Day     string
	Version string
	Sha256  string
}

// HubCopy — скачать копию базы хаба. Пришла не целой (SHA-256 не сходится) — ошибка.
func (c *Client) HubCopy(ctx context.Context) (Copy, error) {
	resp, err := c.do(ctx, "GET", "/api/pc/copy", nil, true)
	if err != nil {
		return Copy{}, err
	}
	if resp.StatusCode == http.StatusConflict {
		resp.Body.Close()
		return Copy{}, ErrNoPassword
	}
	if resp.StatusCode != http.StatusOK {
		return Copy{}, readJSON(resp, nil)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxCopy+1))
	if err != nil {
		return Copy{}, fmt.Errorf("копия не дошла: %w", err)
	}
	if len(b) > MaxCopy {
		return Copy{}, errors.New("копия больше 512 МБ")
	}
	cp := Copy{Data: b, Day: resp.Header.Get("X-Copy-Day"), Version: resp.Header.Get("X-Copy-Version"), Sha256: strings.ToLower(resp.Header.Get("X-Copy-Sha256"))}
	sum := sha256.Sum256(b)
	if cp.Sha256 == "" || hex.EncodeToString(sum[:]) != cp.Sha256 {
		return Copy{}, errors.New("копия пришла не целой (SHA-256 не сходится)")
	}
	return cp, nil
}

// Status — что программа сообщает о себе (Настройки → Компьютеры).
type Status struct {
	Version string         `json:"version"`
	OS      string         `json:"os"`
	Disks   any            `json:"disks"`
	Copies  map[string]any `json:"copies,omitempty"`
	Error   *string        `json:"error"`
	Files   any            `json:"files,omitempty"`
}

func (c *Client) SendStatus(ctx context.Context, s Status) error {
	resp, err := c.do(ctx, "POST", "/api/pc/status", s, true)
	if err != nil {
		return err
	}
	return readJSON(resp, nil)
}

// RateItem — ответ источника курса, как он есть (хаб разбирает сам).
type RateItem struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	Gzip   bool   `json:"gzip,omitempty"`
}

type RateResult struct {
	Source string `json:"source"`
	OK     bool   `json:"ok"`
	Day    string `json:"day,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (c *Client) SendRates(ctx context.Context, items []RateItem) ([]RateResult, error) {
	resp, err := c.do(ctx, "POST", "/api/pc/rates", map[string]any{"items": items}, true)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results []RateResult `json:"results"`
	}
	return r.Results, readJSON(resp, &r)
}

// Source — адрес источника курса из списка хаба.
type Source struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (c *Client) Sources(ctx context.Context) ([]Source, error) {
	resp, err := c.do(ctx, "GET", "/api/pc/rates/sources", nil, false)
	if err != nil {
		return nil, err
	}
	var r struct {
		Sources []Source `json:"sources"`
	}
	return r.Sources, readJSON(resp, &r)
}

// Release — последняя версия программы и SHA-256 её файлов.
type Release struct {
	Version string                  `json:"version"`
	Assets  map[string]ReleaseAsset `json:"assets"`
}

type ReleaseAsset struct {
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
	URL    string `json:"url"`
}

func (c *Client) Latest(ctx context.Context) (Release, error) {
	var r Release
	resp, err := c.do(ctx, "GET", "/api/pc/version", nil, true)
	if err != nil {
		return r, err
	}
	return r, readJSON(resp, &r)
}

// Download — файл программы (хаб отправляет на короткую ссылку GitHub; ключ компьютера туда не уходит: Go не
// переносит Authorization на другой сайт при переадресации).
func (c *Client) Download(ctx context.Context, a ReleaseAsset) ([]byte, error) {
	if !strings.HasPrefix(a.URL, "/api/pc/download/") {
		return nil, errors.New("неверный адрес файла программы")
	}
	resp, err := c.do(ctx, "GET", a.URL, nil, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, readJSON(resp, nil)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownload+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != a.Size || hex.EncodeToString(sum[:]) != strings.ToLower(a.Sha256) {
		return nil, errors.New("файл программы пришёл не целым (размер или SHA-256 не сходятся)")
	}
	return b, nil
}
