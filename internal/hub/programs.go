package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Program — установщик или текст выпуска для «Little Hub/Программы/<Dir>/<Name>» (хаб: GET /api/pc/programs). Файл
// качается по URL: путь хаба (/api/pc/asset/<номер>, /api/pc/programs/sarpo — с ключом компьютера) или прямая
// ссылка на выпуск открытого репозитория плагина на GitHub (без ключа). Text — вместо файла: «Что нового <версия>.txt».
type Program struct {
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	URL    string `json:"url,omitempty"`
	Sha256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Text   string `json:"text,omitempty"`
}

// PluginReleases — откуда хаб даёт прямые ссылки на установщики плагина Strassio (открытый репозиторий выпусков).
const PluginReleases = "https://github.com/little-7o7/strassio-releases/releases/download/"

// MaxProgram — больше установщика в «Программах» не бывает (Little Hub ~10 МБ, плагин ~30 МБ).
const MaxProgram = 200 << 20

func (c *Client) Programs(ctx context.Context) ([]Program, error) {
	var r struct {
		Programs []Program `json:"programs"`
	}
	resp, err := c.do(ctx, "GET", "/api/pc/programs", nil, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNothing // хаб старше: «Программ» ещё нет
	}
	if err := readJSON(resp, &r); err != nil {
		return nil, err
	}
	return r.Programs, nil
}

// assetURL — файл выпуска Little Hub через хаб: только номер файла, никаких других адресов хаба с ключом компьютера.
var assetURL = regexp.MustCompile(`^/api/pc/asset/[0-9]{1,20}$`)

// githubHost — куда GitHub переадресует файл выпуска (короткая ссылка на хранилище файлов).
func githubHost(h string) bool {
	h = strings.ToLower(h)
	return h == "github.com" || strings.HasSuffix(h, ".githubusercontent.com")
}

// ProgramFile — файл программы в w. С хаба — с ключом компьютера (переадресация на GitHub его не уносит:
// noKeyElsewhere); с GitHub — без ключа, переадресации только на github.com и *.githubusercontent.com по https.
func (c *Client) ProgramFile(ctx context.Context, p Program, w io.Writer) (int64, error) {
	var resp *http.Response
	switch {
	case strings.ContainsAny(p.URL, "?#%\\") || strings.Contains(p.URL, ".."):
		return 0, &APIError{Status: 502, Msg: "хаб прислал чужую ссылку на программу"}
	case assetURL.MatchString(p.URL) || p.URL == "/api/pc/programs/sarpo":
		r, err := c.do(ctx, "GET", p.URL, nil, true)
		if err != nil {
			return 0, err
		}
		resp = r
	case strings.HasPrefix(p.URL, PluginReleases):
		req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("User-Agent", fmt.Sprintf("LittleHub/%s (%s; %s)", c.Version, runtime.GOOS, runtime.GOARCH))
		h := http.Client{Timeout: 20 * time.Minute}
		if c.HTTP != nil && c.HTTP.Transport != nil {
			h.Transport = c.HTTP.Transport
		}
		h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("слишком много переадресаций")
			}
			if req.URL.Scheme != "https" || req.URL.User != nil || !githubHost(req.URL.Hostname()) {
				return errors.New("GitHub отправил файл на чужой адрес")
			}
			return nil
		}
		r, err := h.Do(req)
		if err != nil {
			return 0, fmt.Errorf("GitHub не ответил: %w", err)
		}
		resp = r
	default:
		return 0, &APIError{Status: 502, Msg: "хаб прислал чужую ссылку на программу"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return 0, &APIError{Status: resp.StatusCode, Msg: "файл программы не скачался"}
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, MaxProgram+1))
	if err != nil {
		return n, fmt.Errorf("файл программы не дошёл: %w", err)
	}
	if n > MaxProgram {
		return n, &APIError{Status: 502, Msg: "файл программы слишком большой"}
	}
	return n, nil
}
