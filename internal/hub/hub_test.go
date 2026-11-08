package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const token = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

func fake(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("ключ компьютера ушёл на GitHub")
		}
		_, _ = w.Write([]byte("PROGRAM"))
	}))
	t.Cleanup(gh.Close)
	mux := http.NewServeMux()
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/pc/pair", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["code"] != "ABCD1234ABCD1234" || b["name"] == "" || b["os"] == "" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(Paired{Token: token, ID: 3, Name: b["name"], Flag: "/api/pc/flag/k"})
	})
	mux.HandleFunc("GET /api/pc/flag/k", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"v":"5.1.2","u":"7"}`)) })
	mux.HandleFunc("GET /api/pc/copy", authed(func(w http.ResponseWriter, r *http.Request) {
		body := []byte("COPY")
		s := sha256.Sum256(body)
		sha := hex.EncodeToString(s[:])
		if r.URL.Query().Get("bad") != "" {
			sha = strings.Repeat("0", 64)
		}
		w.Header().Set("X-Copy-Day", "2026-11-08")
		w.Header().Set("X-Copy-Version", "5.1.2")
		w.Header().Set("X-Copy-Sha256", sha)
		_, _ = w.Write(body)
	}))
	mux.HandleFunc("POST /api/pc/status", authed(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	mux.HandleFunc("POST /api/pc/rates", authed(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"source":"cbu","ok":true,"day":"2026-11-08"}]}`))
	}))
	mux.HandleFunc("GET /api/pc/rates/sources", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sources":[{"id":"cbu","url":"https://cbu.uz/uz/arkhiv-kursov-valyut/json/"}]}`))
	})
	s := sha256.Sum256([]byte("PROGRAM"))
	mux.HandleFunc("GET /api/pc/version", authed(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{Version: "1.2.0", Assets: map[string]ReleaseAsset{"windows-amd64": {Size: 7, Sha256: hex.EncodeToString(s[:]), URL: "/api/pc/download/windows-amd64"}}})
	}))
	mux.HandleFunc("GET /api/pc/download/windows-amd64", authed(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, gh.URL+"/x", http.StatusFound) }))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &Client{Base: srv.URL, Version: "1.0.0"}
}

func TestPairAndCalls(t *testing.T) {
	ctx := context.Background()
	_, c := fake(t)
	if _, err := c.Pair(ctx, "0000", "PC"); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
	p, err := c.Pair(ctx, "ABCD1234ABCD1234", "OFFICE-PC")
	if err != nil || p.Token != token || p.Flag != "/api/pc/flag/k" {
		t.Fatal(p, err)
	}
	if _, err := c.HubCopy(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatal("без ключа — отключён:", err)
	}
	c.Token = "wrong"
	if _, err := c.HubCopy(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	c.Token = p.Token
	v, err := c.Flag(ctx, p.Flag)
	if err != nil || v.V != "5.1.2" || v.U != "7" {
		t.Fatal(v, err)
	}
	if _, err := c.Flag(ctx, "/x"); err == nil {
		t.Fatal("чужой адрес флага")
	}
	cp, err := c.HubCopy(ctx)
	if err != nil || string(cp.Data) != "COPY" || cp.Day != "2026-11-08" || cp.Version != "5.1.2" {
		t.Fatal(cp, err)
	}
	if err := c.SendStatus(ctx, Status{Version: "1.0.0", Disks: []any{}}); err != nil {
		t.Fatal(err)
	}
	res, err := c.SendRates(ctx, []RateItem{{Source: "cbu", URL: "u", Status: 200, Body: "[]"}})
	if err != nil || len(res) != 1 || !res[0].OK {
		t.Fatal(res, err)
	}
	src, err := c.Sources(ctx)
	if err != nil || len(src) != 1 {
		t.Fatal(src, err)
	}
	rel, err := c.Latest(ctx)
	if err != nil || rel.Version != "1.2.0" {
		t.Fatal(rel, err)
	}
	b, err := c.Download(ctx, rel.Assets["windows-amd64"])
	if err != nil || string(b) != "PROGRAM" {
		t.Fatal(string(b), err)
	}
	bad := rel.Assets["windows-amd64"]
	bad.Sha256 = strings.Repeat("1", 64)
	if _, err := c.Download(ctx, bad); err == nil {
		t.Fatal("чужая сумма")
	}
	bad.URL = "https://evil.example/x"
	if _, err := c.Download(ctx, bad); err == nil {
		t.Fatal("чужой адрес")
	}
}

func TestCopyNotWhole(t *testing.T) {
	_, c := fake(t)
	c.Token = token
	c.Base += "/api/pc/copy?bad=1#"
	if _, err := c.HubCopy(context.Background()); err == nil || !strings.Contains(err.Error(), "не целой") {
		t.Fatal(err)
	}
}

func TestCheckBase(t *testing.T) {
	ok := map[string]string{"https://little-7o7.vercel.app/": "https://little-7o7.vercel.app", " https://hub.test ": "https://hub.test", "http://127.0.0.1:8080": "http://127.0.0.1:8080"}
	for in, want := range ok {
		if got, err := CheckBase(in); err != nil || got != want {
			t.Errorf("%q → %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"http://hub.test", "https://u:p@hub.test", "https://hub.test/x", "https://hub.test?x=1", "ftp://hub.test", ""} {
		if _, err := CheckBase(bad); err == nil {
			t.Errorf("%q должен быть отказ", bad)
		}
	}
}

func TestErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pc/copy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"no-password"}`))
	})
	mux.HandleFunc("/api/pc/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"Неверные данные"}`))
	})
	mux.HandleFunc("/api/pc/version", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: token}
	if _, err := c.HubCopy(context.Background()); !errors.Is(err, ErrNoPassword) {
		t.Fatal(err)
	}
	if err := c.SendStatus(context.Background(), Status{}); err == nil || !strings.Contains(err.Error(), "Неверные данные") {
		t.Fatal(err)
	}
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal(err)
	}
	c.Base = "http://127.0.0.1:1"
	if _, err := c.Sources(context.Background()); err == nil {
		t.Fatal("хаб молчит")
	}
}
