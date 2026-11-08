package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Флаг проекта и копии хаба прошедших дней (план pc-backup-2).

func TestProjectFlag(t *testing.T) {
	ctx := context.Background()
	answer, code := `{"t":1794135900000}`, 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("ключ компьютера ушёл проекту")
		}
		if r.URL.Path == "/api/pc-flag/jump" {
			http.Redirect(w, r, "/api/pc-flag/k", http.StatusFound)
			return
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	c := &Client{Base: "https://hub.example", Token: token, Version: "1.3.0"}
	if v, err := c.ProjectFlag(ctx, srv.URL+"/api/pc-flag/k"); err != nil || v != 1794135900000 {
		t.Fatal(v, err)
	}
	for _, bad := range []string{
		"http://example.com/api/pc-flag/k", // не https
		srv.URL + "/api/pc/flag/k",         // не флаг проекта
		srv.URL + "/api/pc-flag/k?x=1",     // с параметрами
		"https://u:p@example.com/api/pc-flag/k",
		"::",
	} {
		if _, err := c.ProjectFlag(ctx, bad); err == nil || IsAPI(err) {
			t.Fatal("адрес принят", bad, err)
		}
	}
	if _, err := c.ProjectFlag(ctx, srv.URL+"/api/pc-flag/jump"); !IsAPI(err) {
		t.Fatal("переадресация", err)
	}
	code = 404
	if _, err := c.ProjectFlag(ctx, srv.URL+"/api/pc-flag/k"); !IsAPI(err) {
		t.Fatal("404", err)
	}
	code, answer = 200, `{"x":1}`
	if _, err := c.ProjectFlag(ctx, srv.URL+"/api/pc-flag/k"); !IsAPI(err) {
		t.Fatal("без времени", err)
	}
	srv.Close()
	if _, err := c.ProjectFlag(ctx, srv.URL+"/api/pc-flag/k"); err == nil || IsAPI(err) {
		t.Fatal("не ответил", err)
	}
}

func TestHubCopyDays(t *testing.T) {
	ctx := context.Background()
	body := []byte("DAYCOPY")
	s := sha256.Sum256(body)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/pc/copies", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"copies":[{"day":"2026-11-07","bytes":7,"sha256":"` + hex.EncodeToString(s[:]) + `"}]}`))
	})
	mux.HandleFunc("GET /api/pc/copy", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("day") {
		case "2026-11-07":
			w.Header().Set("X-Copy-Day", "2026-11-07")
			w.Header().Set("X-Copy-Sha256", hex.EncodeToString(s[:]))
			_, _ = w.Write(body)
		case "2026-11-05":
			w.WriteHeader(409)
		default:
			w.WriteHeader(404)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := &Client{Base: srv.URL, Token: token, Version: "1.3.0"}
	list, err := c.HubCopies(ctx)
	if err != nil || len(list) != 1 || list[0].Day != "2026-11-07" || list[0].Bytes != 7 {
		t.Fatal(list, err)
	}
	cp, err := c.HubCopyDay(ctx, "2026-11-07")
	if err != nil || cp.Day != "2026-11-07" || string(cp.Data) != "DAYCOPY" {
		t.Fatal(cp, err)
	}
	if _, err := c.HubCopyDay(ctx, "2026-11-06"); !errors.Is(err, ErrNothing) {
		t.Fatal("нет дня", err)
	}
	if _, err := c.HubCopyDay(ctx, "2026-11-05"); !errors.Is(err, ErrNoPassword) {
		t.Fatal("нет пароля", err)
	}
	if _, err := c.HubCopyDay(ctx, "../x"); err == nil {
		t.Fatal("неверный день принят")
	}
	c.Token = "wrong"
	if _, err := c.HubCopies(ctx); err == nil {
		t.Fatal("без ключа")
	}
}

func TestProjectCalls(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/pc/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		only := r.URL.Query().Get("only")
		lock := `null`
		switch only {
		case "lock":
			lock = `{"pub":"qfnUcDh2nSBcOPSzOjaESq10eHwIwBkunNw5WZyasTI=","salt":"AAECAwQFBgcICQoLDA0ODw==","iterations":1000}`
		case "badlock":
			lock = `{"pub":"AAAA","salt":"AAECAwQFBgcICQoLDA0ODw==","iterations":1000}`
		}
		_, _ = w.Write([]byte(`{"projects":[{"slug":"` + only + `x","folder":"F","ok":true,"live":true,"flag":"https://p/api/pc-flag/k","businesses":[]}],"lock":` + lock + `}`))
	})
	mux.HandleFunc("GET /api/pc/p/sarpo/items", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("b") {
		case "1":
			_, _ = w.Write([]byte(`{"groups":[1]}`))
		case "2":
			_, _ = w.Write([]byte(`не json`))
		case "3":
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":"no-password"}`))
		default:
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":"x","message":"Проект упал"}`))
		}
	})
	mux.HandleFunc("GET /api/pc/p/sarpo/photo", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("p") {
		case "1":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("JPEG-12345"))
		case "2":
			w.WriteHeader(404)
		case "3":
			w.WriteHeader(204)
		default:
			w.WriteHeader(502)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := &Client{Base: srv.URL, Token: token, Version: "1.3.0"}
	ps, lock, err := c.Projects(ctx, "sarpo")
	if err != nil || len(ps) != 1 || ps[0].Slug != "sarpox" || !ps[0].Live || ps[0].Flag == "" || lock != nil {
		t.Fatal(ps, err)
	}
	// Ключ шифрования файлов — образец хаба (tests/pc-now.test.ts).
	if _, lock, err := c.Projects(ctx, "lock"); err != nil || lock == nil || hex.EncodeToString(lock.Pub) != "a9f9d47038769d205c38f4b33a36844aad74787c08c0192e9cdc39599c9ab132" || lock.Iter != 1000 {
		t.Fatal("ключ", lock, err)
	}
	if _, _, err := c.Projects(ctx, "badlock"); !IsAPI(err) {
		t.Fatal("кривой ключ", err)
	}
	if _, _, err := c.Projects(ctx, "../x"); err == nil {
		t.Fatal("неверный проект принят")
	}
	var out struct{ Groups []int }
	if err := c.ProjectJSON(ctx, "sarpo", "items", url.Values{"b": {"1"}}, &out); err != nil || len(out.Groups) != 1 {
		t.Fatal(out, err)
	}
	if err := c.ProjectJSON(ctx, "sarpo", "items", url.Values{"b": {"2"}}, &out); !IsAPI(err) {
		t.Fatal("не JSON", err)
	}
	if err := c.ProjectJSON(ctx, "sarpo", "items", url.Values{"b": {"3"}}, &out); !errors.Is(err, ErrNoPassword) {
		t.Fatal("нет пароля", err)
	}
	var ae *APIError
	if err := c.ProjectJSON(ctx, "sarpo", "items", url.Values{"b": {"4"}}, &out); !errors.As(err, &ae) || ae.Status != 500 || ae.Msg != "Проект упал" || ae.Error() == "" {
		t.Fatal("ошибка проекта", err)
	}
	if err := c.ProjectJSON(ctx, "Sarpo/..", "items", nil, &out); err == nil || IsAPI(err) {
		t.Fatal("неверный адрес принят", err)
	}
	var buf bytes.Buffer
	s, err := c.ProjectSave(ctx, "sarpo", "photo", url.Values{"p": {"1"}}, &buf, 1<<20)
	if err != nil || s.Type != "image/jpeg" || s.Bytes != 10 || buf.String() != "JPEG-12345" {
		t.Fatal(s, err)
	}
	if _, err := c.ProjectSave(ctx, "sarpo", "photo", url.Values{"p": {"1"}}, &bytes.Buffer{}, 5); !IsAPI(err) {
		t.Fatal("больше предела", err)
	}
	for _, p := range []string{"2", "3"} {
		if _, err := c.ProjectSave(ctx, "sarpo", "photo", url.Values{"p": {p}}, &bytes.Buffer{}, 1<<20); !errors.Is(err, ErrNothing) {
			t.Fatal("нечего", p, err)
		}
	}
	if _, err := c.ProjectSave(ctx, "sarpo", "photo", url.Values{"p": {"9"}}, &bytes.Buffer{}, 1<<20); !IsAPI(err) {
		t.Fatal("502", err)
	}
}
