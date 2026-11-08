package ui

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/app"
	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/secret"
)

func server(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, _ := config.Load(dir)
	a := &app.App{Version: "1.0.0", Dir: dir, Cfg: cfg, Secret: secret.New(dir), Space: func(string) (disks.Space, error) { return disks.Space{Free: 5e11, Total: 1e12}, nil }}
	quit := make(chan struct{}, 1)
	s, ln, err := Listen(a, dir, func() { quit <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(s.Close)
	return s, dir
}

func req(t *testing.T, s *Server, method, path, host string, hdr map[string]string, body []byte) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", s.Port, path), bytes.NewReader(body))
	if host != "" {
		r.Host = host
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	c := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 5 * time.Second}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestAccess(t *testing.T) {
	s, dir := server(t)
	info, err := ReadInfo(dir)
	if err != nil || info.Port != s.Port || info.Key != s.Key {
		t.Fatal(info, err)
	}
	st, _ := os.Stat(dir + "/ui.json")
	if st.Mode().Perm() != 0o600 {
		t.Fatal("ui.json доступен другим")
	}
	if !info.Alive() {
		t.Fatal("окно не отвечает")
	}
	// Без ключа — 401; чужой Host (подмена DNS) — 403.
	if r := req(t, s, "GET", "/api/state", "", nil, nil); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	if r := req(t, s, "GET", "/api/state", "evil.example:80", map[string]string{"X-LH-Key": s.Key}, nil); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	// Ссылка с ключом → cookie и переход на «/».
	r := req(t, s, "GET", "/?k="+s.Key, "", nil, nil)
	if r.StatusCode != 303 || !strings.Contains(r.Header.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(r.Header.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatal(r.StatusCode, r.Header)
	}
	if r := req(t, s, "GET", "/?k=wrong", "", nil, nil); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	ck := map[string]string{"Cookie": cookie + "=" + s.Key}
	if r := req(t, s, "GET", "/", "", ck, nil); r.StatusCode != 200 || r.Header.Get("Content-Security-Policy") == "" {
		t.Fatal(r.StatusCode)
	}
	if r := req(t, s, "GET", "/api/state", "localhost:"+fmt.Sprint(s.Port), ck, nil); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	// POST — только с X-LH и своим Origin.
	if r := req(t, s, "POST", "/api/save", "", ck, nil); r.StatusCode != 403 {
		t.Fatal("без X-LH", r.StatusCode)
	}
	if r := req(t, s, "POST", "/api/save", "", map[string]string{"Cookie": ck["Cookie"], "X-LH": "1", "Origin": "https://evil.example"}, nil); r.StatusCode != 403 {
		t.Fatal("чужой Origin", r.StatusCode)
	}
	if r := req(t, s, "POST", "/api/save", "", map[string]string{"Cookie": ck["Cookie"], "X-LH": "1", "Origin": fmt.Sprintf("http://127.0.0.1:%d", s.Port)}, nil); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	post := map[string]string{"Cookie": ck["Cookie"], "X-LH": "1"}
	if r := req(t, s, "POST", "/api/lang", "", post, []byte(`{"lang":"en"}`)); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := req(t, s, "POST", "/api/lang", "", post, []byte(`{"lang":"xx"}`)); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	d := t.TempDir()
	if r := req(t, s, "POST", "/api/disks", "", post, []byte(`{"disks":["`+d+`"]}`)); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := req(t, s, "POST", "/api/disks", "", post, []byte(`{"disks":[]}`)); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	if r := req(t, s, "POST", "/api/pair", "", post, []byte(`{"code":"1","hub":"https://hub.test"}`)); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	for _, p := range []string{"/app.js", "/app.css", "/icon.png"} {
		if r := req(t, s, "GET", p, "", ck, nil); r.StatusCode != 200 {
			t.Fatal(p, r.StatusCode)
		}
	}
	if !info.Quit() {
		t.Fatal("выход")
	}
}

func TestDecrypt(t *testing.T) {
	s, _ := server(t)
	file, _ := os.ReadFile("../copyfmt/testdata/hub.l7copy")
	send := func(pass string) *http.Response {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		fw, _ := w.CreateFormFile("file", "hub-2026-11-08.l7copy")
		_, _ = fw.Write(file)
		_ = w.WriteField("password", pass)
		_ = w.Close()
		return req(t, s, "POST", "/api/decrypt", "", map[string]string{"X-LH-Key": s.Key, "X-LH": "1", "Content-Type": w.FormDataContentType()}, b.Bytes())
	}
	r := send("образец-пароля-копий")
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Disposition"), "hub-2026-11-08.sql") {
		t.Fatal(r.StatusCode, r.Header)
	}
	if r := send("не тот"); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	if r := req(t, s, "POST", "/api/decrypt", "", map[string]string{"X-LH-Key": s.Key, "X-LH": "1"}, nil); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
}

func TestHandlerDirect(t *testing.T) {
	s := &Server{Port: 1, Key: "k"}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:1/ping", nil))
	if rec.Body.String() != "littlehub" {
		t.Fatal(rec.Body.String())
	}
	if (Info{Port: 1}).Alive() || (Info{Port: 1}).Quit() {
		t.Fatal("никого нет")
	}
	if !strings.HasPrefix((Info{Port: 5, Key: "x"}).URL(), "http://127.0.0.1:5/?k=x") {
		t.Fatal("ссылка")
	}
}
