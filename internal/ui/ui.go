// Package ui — окно программы: страница на http://127.0.0.1:<порт>/, только для этого компьютера. Вход — одноразовая
// ссылка с ключом (значок → «Открыть»): страница ставит cookie и дальше ходит с ним. Чужой Host (подмена DNS) и
// чужой Origin — отказ; изменения — только POST с заголовком X-LH (чужая страница в браузере так не может).
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/app"
	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
)

//go:embed web
var web embed.FS

const cookie = "lh"

// Ports — порты окна по порядку: первый свободный.
var Ports = []int{47613, 47614, 47615, 47616}

// Info — где окно этой программы (ui.json рядом с настройками, права — только владельцу).
type Info struct {
	Port int    `json:"port"`
	Key  string `json:"key"`
	PID  int    `json:"pid"`
}

func infoPath(dir string) string { return filepath.Join(dir, "ui.json") }

// ReadInfo — окно уже запущенной программы.
func ReadInfo(dir string) (Info, error) {
	var i Info
	b, err := os.ReadFile(infoPath(dir))
	if err != nil {
		return i, err
	}
	return i, json.Unmarshal(b, &i)
}

// URL — ссылка входа в окно.
func (i Info) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/?k=%s", i.Port, i.Key) }

// Alive — отвечает ли окно по этому адресу (вторая копия программы откроет его и выйдет).
func (i Info) Alive() bool {
	c := http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/ping", i.Port), nil)
	req.Header.Set("X-LH-Key", i.Key)
	r, err := c.Do(req)
	if err != nil {
		return false
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 64))
	return r.StatusCode == 200 && string(b) == "littlehub"
}

// Quit — попросить запущенную программу выйти (переустановка).
func (i Info) Quit() bool {
	c := http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/quit", i.Port), nil)
	req.Header.Set("X-LH-Key", i.Key)
	req.Header.Set("X-LH", "1")
	r, err := c.Do(req)
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == 200
}

// Server — окно.
type Server struct {
	App  *app.App
	Key  string
	Port int
	Quit func()
	srv  *http.Server
}

// Listen — занять порт и записать ui.json.
func Listen(a *app.App, dir string, quit func()) (*Server, net.Listener, error) {
	var ln net.Listener
	var err error
	for _, p := range Ports {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			break
		}
	}
	if ln == nil {
		return nil, nil, fmt.Errorf("порты окна заняты: %w", err)
	}
	k := make([]byte, 24)
	_, _ = rand.Read(k)
	s := &Server{App: a, Key: hex.EncodeToString(k), Port: ln.Addr().(*net.TCPAddr).Port, Quit: quit}
	b, _ := json.Marshal(Info{Port: s.Port, Key: s.Key, PID: os.Getpid()})
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(infoPath(dir), b, 0o600)
	}
	return s, ln, nil
}

// Serve — работать до выхода.
func (s *Server) Serve(ln net.Listener) error {
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	err := s.srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Close() {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
	}
}

// Info — для ссылки «Открыть».
func (s *Server) Info() Info { return Info{Port: s.Port, Key: s.Key, PID: os.Getpid()} }

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) hostOK(r *http.Request) bool {
	return r.Host == fmt.Sprintf("127.0.0.1:%d", s.Port) || r.Host == fmt.Sprintf("localhost:%d", s.Port)
}

func (s *Server) authed(r *http.Request) bool {
	if k := r.Header.Get("X-LH-Key"); k != "" {
		return same(k, s.Key)
	}
	c, err := r.Cookie(cookie)
	return err == nil && same(c.Value, s.Key)
}

func (s *Server) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == fmt.Sprintf("http://127.0.0.1:%d", s.Port) || o == fmt.Sprintf("http://localhost:%d", s.Port)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler — все адреса окна.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("littlehub")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { s.static(w, "web/index.html") })
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) { s.static(w, "web/app.js") })
	mux.HandleFunc("GET /app.css", func(w http.ResponseWriter, r *http.Request) { s.static(w, "web/app.css") })
	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, r *http.Request) { s.static(w, "web/icon.png") })
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.App.View()) })
	mux.HandleFunc("POST /api/pair", s.pair)
	mux.HandleFunc("POST /api/save", func(w http.ResponseWriter, r *http.Request) {
		s.App.Kick()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/rates", func(w http.ResponseWriter, r *http.Request) {
		go func() { _ = s.App.SendRates(context.Background()) }()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/disks", s.disks)
	mux.HandleFunc("POST /api/lang", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Lang string }
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&b)
		if err := s.App.SetLang(b.Lang); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/decrypt", s.decrypt)
	mux.HandleFunc("POST /api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
		if s.Quit != nil {
			go func() {
				time.Sleep(200 * time.Millisecond)
				s.Quit()
			}()
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; form-action 'self'")
		if !s.hostOK(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Ссылка «Открыть» с ключом: ставим cookie и убираем ключ из адреса.
		if r.Method == "GET" && r.URL.Path == "/" && r.URL.Query().Has("k") {
			if !same(r.URL.Query().Get("k"), s.Key) {
				http.Error(w, "Ссылка устарела — откройте окно через значок Little Hub.", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookie, Value: s.Key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if r.URL.Path != "/ping" && !s.authed(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Little Hub</title><p style="font:16px system-ui;padding:24px">Откройте окно через значок Little Hub (у часов на Windows, в строке меню на Mac).</p>`)
			return
		}
		if r.Method == "POST" && (!s.originOK(r) || r.Header.Get("X-LH") != "1") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) static(w http.ResponseWriter, name string) {
	b, err := web.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
	_, _ = w.Write(b)
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var b struct{ Code, Hub string }
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&b)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := s.App.Pair(ctx, b.Hub, b.Code)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"name": p.Name})
}

func (s *Server) disks(w http.ResponseWriter, r *http.Request) {
	var b struct{ Disks []string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "неверные данные"})
		return
	}
	if err := s.App.SetDisks(b.Disks); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// decrypt — «Расшифровать…»: файл копии и пароль копий → SQL (скачивается в браузере). Файл и пароль не сохраняются.
func (s *Server) decrypt(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, 400, map[string]string{"error": "файл не дошёл"})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	f, h, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "выберите файл копии"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "файл не прочитался"})
		return
	}
	plain, err := copyfmt.Open(data, r.FormValue("password"))
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	name := strings.TrimSuffix(filepath.Base(h.Filename), filepath.Ext(h.Filename)) + ".sql"
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '/' || r < 32 {
			return '_'
		}
		return r
	}, name)
	w.Header().Set("Content-Type", "application/sql; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	_, _ = w.Write(plain)
}
