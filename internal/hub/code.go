package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// CodeRepo — репозиторий владельца на GitHub для «Little Hub/Код/<Name>/» (хаб: GET /api/pc/code; план pc-backup-2,
// этап 4, пункты 89–90). PushedAt меняется после каждого push — тогда программа берёт новый архив кода.
type CodeRepo struct {
	Name     string `json:"name"`
	Private  bool   `json:"private"`
	Branch   string `json:"branch"`
	PushedAt string `json:"pushedAt"`
	Size     int64  `json:"size"`
	Archived bool   `json:"archived"`
}

// CodeHost — куда хаб отправляет за архивом кода: короткая ссылка GitHub с временным ключом внутри.
const CodeHost = "codeload.github.com"

// MaxCode — больше архива кода не бывает (самый большой репозиторий — десятки МБ).
const MaxCode = 1 << 30

var codeName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// Code — репозитории (хаб без ключа GitHub — пусто). Хаб старше — ErrNothing.
func (c *Client) Code(ctx context.Context) ([]CodeRepo, error) {
	var r struct {
		On    bool       `json:"on"`
		Repos []CodeRepo `json:"repos"`
	}
	resp, err := c.do(ctx, "GET", "/api/pc/code", nil, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNothing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	if err := readJSON(resp, &r); err != nil {
		return nil, err
	}
	if !r.On {
		return nil, nil
	}
	return r.Repos, nil
}

// CodeFile — архив кода (what = "zip") или «<репозиторий>.xlsx» (what = "excel") в w. Архив хаб не отдаёт сам, а
// переадресует на codeload.github.com: туда идём без ключа компьютера и только по https, дальше переадресаций нет.
// 404 — ErrNothing (репозитория больше нет или он пустой).
func (c *Client) CodeFile(ctx context.Context, repo, what string, w io.Writer, max int64) (Saved, error) {
	if !codeName.MatchString(repo) || repo == "." || repo == ".." || (what != "zip" && what != "excel") {
		return Saved{}, &APIError{Status: 400, Msg: "неверное имя репозитория"}
	}
	if c.Token == "" {
		return Saved{}, ErrRevoked
	}
	agent := fmt.Sprintf("LittleHub/%s (%s; %s)", c.Version, runtime.GOOS, runtime.GOARCH)
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+"/api/pc/code/"+url.PathEscape(repo)+"/"+what, nil)
	if err != nil {
		return Saved{}, err
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	h := c.client()
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := h.Do(req)
	if err != nil {
		return Saved{}, fmt.Errorf("хаб не ответил: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		resp.Body.Close()
		return Saved{}, ErrRevoked
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return Saved{}, ErrNothing
	case what == "zip" && resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		u, err := url.Parse(loc)
		if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Hostname(), CodeHost) || u.Port() != "" {
			return Saved{}, &APIError{Status: 502, Msg: "хаб отправил за кодом на чужой адрес"}
		}
		g, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return Saved{}, err
		}
		g.Header.Set("User-Agent", agent)
		gh := http.Client{Timeout: 30 * time.Minute}
		if c.HTTP != nil && c.HTTP.Transport != nil {
			gh.Transport = c.HTTP.Transport
		}
		gh.CheckRedirect = func(*http.Request, []*http.Request) error {
			return errors.New("GitHub переадресовал архив кода")
		}
		if resp, err = gh.Do(g); err != nil {
			return Saved{}, fmt.Errorf("GitHub не ответил: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Saved{}, apiError(resp)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return Saved{}, fmt.Errorf("файл кода не дошёл: %w", err)
	}
	if n > max {
		return Saved{}, &APIError{Status: 502, Msg: "файл кода слишком большой"}
	}
	return Saved{Type: resp.Header.Get("Content-Type"), Bytes: n, Header: resp.Header}, nil
}
