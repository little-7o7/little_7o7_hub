package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
	"github.com/little-7o7/little_7o7_hub/internal/secret"
)

const tok = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

type fakeHub struct {
	mu      sync.Mutex
	copy    []byte
	day     string
	version string
	status  []map[string]any
	rates   int
	copies  int
	noPass  bool
	revoked bool
	paired  bool
	sources string
}

func (f *fakeHub) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			rev := f.revoked
			f.mu.Unlock()
			if rev || r.Header.Get("Authorization") != "Bearer "+tok {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/pc/pair", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["code"] != "ABCD1234ABCD1234" {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.paired = true
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(hub.Paired{Token: tok, ID: 1, Name: b["name"], Flag: "/api/pc/flag/k"})
	})
	mux.HandleFunc("GET /api/pc/flag/k", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"v": f.version})
	})
	mux.HandleFunc("GET /api/pc/copy", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.noPass {
			w.WriteHeader(409)
			return
		}
		f.copies++
		s := sha256.Sum256(f.copy)
		w.Header().Set("X-Copy-Day", f.day)
		w.Header().Set("X-Copy-Version", f.version)
		w.Header().Set("X-Copy-Sha256", hex.EncodeToString(s[:]))
		_, _ = w.Write(f.copy)
	}))
	mux.HandleFunc("POST /api/pc/status", auth(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.status = append(f.status, b)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	mux.HandleFunc("GET /api/pc/rates/sources", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(f.sources)) })
	mux.HandleFunc("POST /api/pc/rates", auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Items []hub.RateItem `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.rates += len(b.Items)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"results":[{"source":"cbu","ok":true,"day":"2026-11-08"},{"source":"nbu","ok":false,"error":"далеко"}]}`))
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newApp(t *testing.T, f *fakeHub, now *time.Time) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	disk := t.TempDir()
	a := &App{Version: "1.0.0", Dir: dir, Cfg: cfg, Secret: secret.New(dir), Now: func() time.Time { return *now },
		Space: func(string) (disks.Space, error) { return disks.Space{Free: 500e9, Total: 1e12}, nil }}
	srv := f.server(t)
	if _, err := a.Pair(context.Background(), srv.URL, "abcd-1234-abcd-1234"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetDisks([]string{disk}); err != nil {
		t.Fatal(err)
	}
	return a, disk
}

func sample(t *testing.T) []byte {
	b, err := os.ReadFile("../copyfmt/testdata/hub.l7copy")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPairBadCode(t *testing.T) {
	f := &fakeHub{}
	srv := f.server(t)
	dir := t.TempDir()
	cfg, _ := config.Load(dir)
	a := &App{Version: "1.0.0", Cfg: cfg, Secret: secret.New(dir)}
	if a.Paired() {
		t.Fatal("ещё не подключён")
	}
	if _, err := a.Pair(context.Background(), srv.URL, "1234"); !errors.Is(err, hub.ErrBadCode) {
		t.Fatal(err)
	}
	if _, err := a.Pair(context.Background(), srv.URL, "0000-0000-0000-0000"); !errors.Is(err, hub.ErrBadCode) {
		t.Fatal(err)
	}
	if _, err := a.Pair(context.Background(), "http://evil.example", "abcd-1234-abcd-1234"); err == nil {
		t.Fatal("http — нельзя")
	}
}

func TestCopyDayFileAndFreeze(t *testing.T) {
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC) // 23:00 в Ташкенте
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "5.1.1", sources: `{"sources":[]}`}
	a, disk := newApp(t, f, &now)
	ctx := context.Background()
	if err := a.CopyHub(ctx); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(disks.Root(disk), "Хаб", "Копии базы", "2026-11", "hub-2026-11-08.l7copy")
	if b, err := os.ReadFile(day); err != nil || string(b) != string(f.copy) {
		t.Fatal(err)
	}
	if c := a.Cfg.Get().Copies["hub"]; c.Day != "2026-11-08" || c.Version != "5.1.1" || c.Disk != disk {
		t.Fatalf("%+v", c)
	}
	// Изменение в тот же день — тот же файл заменяется.
	changed := append([]byte(nil), f.copy...)
	changed[len(changed)-1] ^= 1
	f.mu.Lock()
	f.copy, f.version = changed, "5.1.2"
	f.mu.Unlock()
	if err := a.CopyHub(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(day); string(b) != string(changed) {
		t.Fatal("файл дня не заменён")
	}
	// После полуночи — новый файл; вчерашний не трогаем.
	now = time.Date(2026, 11, 8, 19, 30, 0, 0, time.UTC)
	f.mu.Lock()
	f.day, f.copy = "2026-11-09", sample(t)
	f.mu.Unlock()
	if err := a.CopyHub(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(day); string(b) != string(changed) {
		t.Fatal("вчерашний файл изменился")
	}
	if _, err := os.Stat(filepath.Join(disks.Root(disk), "Хаб", "Копии базы", "2026-11", "hub-2026-11-09.l7copy")); err != nil {
		t.Fatal(err)
	}
	// Копия «за прошлый месяц» и не копия — отказ.
	f.mu.Lock()
	f.day = "2026-10-01"
	f.mu.Unlock()
	if err := a.CopyHub(ctx); err == nil || !strings.Contains(err.Error(), "часы") {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.day, f.copy = "2026-11-09", []byte("not a copy")
	f.mu.Unlock()
	if err := a.CopyHub(ctx); err == nil {
		t.Fatal("не копия")
	}
	if v := a.View(); v.Error == "" || !v.Paired {
		t.Fatalf("%+v", v)
	}
}

func TestDiskMovesMidDay(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`}
	a, first := newApp(t, f, &now)
	second := t.TempDir()
	_ = a.SetDisks([]string{first, second})
	full := false
	a.Space = func(p string) (disks.Space, error) {
		if p == first && full {
			return disks.Space{Free: 1e9, Total: 1e12}, nil
		}
		return disks.Space{Free: 500e9, Total: 1e12}, nil
	}
	ctx := context.Background()
	if err := a.CopyHub(ctx); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("Хаб", "Копии базы", "2026-11", "hub-2026-11-08.l7copy")
	full = true
	if err := a.CopyHub(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(disks.Root(second), rel)); err != nil {
		t.Fatal("нет на втором диске")
	}
	if _, err := os.Stat(filepath.Join(disks.Root(first), rel)); !os.IsNotExist(err) {
		t.Fatal("файл дня остался и на первом диске")
	}
	a.Space = func(string) (disks.Space, error) { return disks.Space{}, errors.New("нет") }
	if err := a.CopyHub(ctx); !errors.Is(err, disks.ErrNoDisk) {
		t.Fatal(err)
	}
	if st := a.Status(); st.Error == nil {
		t.Fatal("ошибка дисков не дошла до хаба")
	}
}

func TestTickFlagAndMissingDay(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`}
	a, _ := newApp(t, f, &now)
	ctx := context.Background()
	// Сегодняшнего файла нет — копия на первом же шаге.
	a.tick(ctx)
	if f.copies != 1 {
		t.Fatal("нет копии за сегодня", f.copies)
	}
	// Флаг не менялся — не копируем.
	now = now.Add(2 * time.Minute)
	a.tick(ctx)
	if f.copies != 1 {
		t.Fatal(f.copies)
	}
	// Изменился — ждём 2 минуты затишья.
	f.mu.Lock()
	f.version = "2"
	f.mu.Unlock()
	now = now.Add(2 * time.Minute)
	a.tick(ctx)
	if f.copies != 1 || a.View().Waiting == "" {
		t.Fatal("сразу не копируем")
	}
	now = now.Add(2 * time.Minute)
	a.tick(ctx)
	if f.copies != 2 {
		t.Fatal("после затишья — копия", f.copies)
	}
	if len(f.status) == 0 {
		t.Fatal("состояние не ушло")
	}
	// Пароль копий не задан — ошибка видна, повтор не раньше чем через 30 минут.
	f.mu.Lock()
	f.noPass, f.version = true, "3"
	f.mu.Unlock()
	a.Kick()
	if err := a.CopyHub(ctx); !errors.Is(err, hub.ErrNoPassword) || !a.View().NoPass {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	a.tick(ctx)
	// Отключили в хабе.
	f.mu.Lock()
	f.revoked, f.noPass = true, false
	f.mu.Unlock()
	if err := a.CopyHub(ctx); !errors.Is(err, hub.ErrRevoked) {
		t.Fatal(err)
	}
	if a.Paired() || !a.View().Revoked {
		t.Fatal("отключён")
	}
}

func TestRatesAndView(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer site.Close()
	f := &fakeHub{sources: `{"sources":[]}`}
	a, _ := newApp(t, f, &now)
	// Сайты курса подменяем: все запросы — на тестовый сервер.
	a.HTTP = &http.Client{Transport: rewrite{site.URL}}
	if err := a.SendRates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.rates != 3 {
		t.Fatal("ушло источников:", f.rates)
	}
	v := a.View()
	if len(v.Rates) != 2 || v.RatesAt == "" || len(v.Log) == 0 {
		t.Fatalf("%+v", v)
	}
	if err := a.SetLang("en"); err != nil || a.View().Lang != "en" {
		t.Fatal(err)
	}
	if a.SetLang("de") == nil {
		t.Fatal("нет такого языка")
	}
	if a.SetDisks([]string{"relative"}) == nil || a.SetDisks(nil) == nil {
		t.Fatal("плохие диски")
	}
	if ComputerName() == "" {
		t.Fatal("нет имени")
	}
	TrimLog(a.Dir)
}

type rewrite struct{ to string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	n, _ := http.NewRequest(req.Method, r.to+u.Path, nil)
	return http.DefaultTransport.RoundTrip(n)
}
