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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/files"
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
	// Файлы проектов: ответ /api/pc/projects и ответы /api/pc/p/<проект>/<что>?… по «что?запрос».
	projects string
	answers  map[string]string
	asked    []string
	// ask — просьба «Обновить программу» во флаге; latest — версия в /api/pc/version, checks — сколько раз спросили.
	ask    string
	latest string
	checks int
	// sync — просьба «Сохранить сейчас на всех»; days — копии хаба прошедших дней; hdrs — заголовки ответа проекта по
	// «что?запрос» (вместо обычных у копии Sarpo); only — какие проекты спросили (?only=).
	sync  string
	days  []string
	hdrs  map[string]map[string]string
	only  []string
	dayed int
	// programs — ответ /api/pc/programs ("" — 404, хаб старше), assets — файлы /api/pc/asset/<номер>, progAsked —
	// сколько раз спросили список.
	programs  string
	assets    map[string]string
	progAsked int
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
		_ = json.NewEncoder(w).Encode(map[string]string{"v": f.version, "u": f.ask, "s": f.sync})
	})
	mux.HandleFunc("GET /api/pc/version", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.checks++
		_ = json.NewEncoder(w).Encode(hub.Release{Version: f.latest})
	}))
	mux.HandleFunc("GET /api/pc/copy", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.noPass {
			w.WriteHeader(409)
			return
		}
		day := f.day
		if d := r.URL.Query().Get("day"); d != "" {
			found := false
			for _, x := range f.days {
				found = found || x == d
			}
			if !found {
				w.WriteHeader(404)
				return
			}
			day = d
			f.dayed++
		} else {
			f.copies++
		}
		s := sha256.Sum256(f.copy)
		w.Header().Set("X-Copy-Day", day)
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
	mux.HandleFunc("GET /api/pc/copies", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		list := []hub.DayCopy{}
		for _, d := range f.days {
			list = append(list, hub.DayCopy{Day: d})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"copies": list})
	}))
	mux.HandleFunc("GET /api/pc/projects", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.only = append(f.only, r.URL.Query().Get("only"))
		_, _ = w.Write([]byte(f.projects))
	}))
	mux.HandleFunc("GET /api/pc/p/{slug}/{op}", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		k := r.PathValue("slug") + "/" + r.PathValue("op") + "?" + r.URL.RawQuery
		f.asked = append(f.asked, k)
		a, ok := f.answers[k]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"нет"}`))
			return
		}
		if strings.HasPrefix(k, "sarpo/backup?") {
			w.Header().Set("X-Copy-Name", "sarpo-2026-11-07.sql.gz")
			w.Header().Set("X-Copy-Day", "2026-11-07")
			w.Header().Set("X-Copy-Bytes", strconv.Itoa(len(a)))
		}
		for h, v := range f.hdrs[k] {
			w.Header().Set(h, v)
		}
		_, _ = w.Write([]byte(a))
	}))
	mux.HandleFunc("GET /api/pc/programs", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.progAsked++
		if f.programs == "" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(f.programs))
	}))
	mux.HandleFunc("GET /api/pc/asset/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		a, ok := f.assets[r.PathValue("id")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(a))
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

func TestSyncFiles(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	copyFile := string(sample(t))
	item := `{"items":[{"id":1,"files":[{"name":"Всё о клиенте.xlsx","data":"eA=="}]}],"more":[]}`
	f := &fakeHub{
		projects: `{"projects":[{"slug":"sarpo","folder":"Sarpo Business","copy":true,"ok":true,"businesses":[{"id":7,"slug":"sarpotextile","name":"Sarpo Textile","kind":"shop","full":true,"ver":"v1","files":[{"key":"state","folder":"Excel","name":"Магазин-2026-11-08.xlsx","refresh":false}]}]},{"slug":"strassio","folder":"Strassio","copy":false,"ok":false,"error":"не подключён","businesses":[]}]}`,
		answers: map[string]string{
			"sarpo/excel?b=7&f=state":           "xlsx",
			"sarpo/items?arch=1&b=7":            `{"groups":[{"kind":"client","folder":"Клиенты","archive":"Клиенты (архив)","files":["Всё о клиенте.xlsx"],"items":[{"id":1,"dir":"Ахмед","ver":"1"}]}]}`,
			"sarpo/items?b=7&ids=1&kind=client": item,
			"sarpo/backup?":                     copyFile,
		},
		programs: `{"programs":[{"dir":"Little Hub","name":"LittleHub-1.2.1-windows.exe","url":"/api/pc/asset/21","size":4},{"dir":"Little Hub","name":"Что нового 1.2.1.txt","text":"Что нового"}]}`,
		assets:   map[string]string{"21": "MZ21"},
	}
	a, disk := newApp(t, f, &now)
	if err := a.SyncFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	biz := filepath.Join(disk, "Little Hub", "Sarpo Business", "sarpotextile")
	if b, _ := os.ReadFile(filepath.Join(biz, "Excel", "Магазин-2026-11-08.xlsx")); string(b) != "xlsx" {
		t.Fatal("нет Excel")
	}
	if b, _ := os.ReadFile(filepath.Join(biz, "Клиенты", "Ахмед", "Всё о клиенте.xlsx")); string(b) != "x" {
		t.Fatal("нет папки клиента")
	}
	cp := filepath.Join(disk, "Little Hub", "Sarpo Business", "Копии базы", "2026-11", "sarpo-2026-11-07.l7copy")
	if b, _ := os.ReadFile(cp); string(b) != copyFile {
		t.Fatal("нет копии базы Sarpo")
	}
	cfg := a.Cfg.Get()
	if cfg.Copies["sarpo"].Version != "sarpo-2026-11-07.sql.gz" || cfg.Files == nil || cfg.Files.Projects["sarpo"].Files < 2 || cfg.Files.Projects["sarpo"].Businesses != 1 {
		t.Fatalf("итог: %+v %+v", cfg.Copies["sarpo"], cfg.Files)
	}
	if es := cfg.Files.Projects["strassio"].Errors; len(es) != 1 || !strings.Contains(es[0], "не подключён") {
		t.Fatalf("неподключённый проект: %+v", cfg.Files.Projects["strassio"])
	}
	// «Программы» — открыто, рядом «Что сохранилось сегодня.txt».
	if b, _ := os.ReadFile(filepath.Join(disk, "Little Hub", "Программы", "Little Hub", "LittleHub-1.2.1-windows.exe")); string(b) != "MZ21" {
		t.Fatal("нет установщика в «Программах»")
	}
	report, _ := os.ReadFile(filepath.Join(disk, "Little Hub", TodayFile))
	for _, want := range []string{"что сохранилось сегодня, 08.11.2026", "• Хаб — ", "• Sarpo Business: файлов ", "• Программы: файлов 2", "не подключён"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("в отчёте дня нет %q:\n%s", want, report)
		}
	}
	// Хабу ушло состояние с файлами.
	f.mu.Lock()
	last := f.status[len(f.status)-1]
	f.mu.Unlock()
	if _, ok := last["files"]; !ok {
		t.Fatal("в состоянии нет файлов")
	}
	// Второй раз: та же версия бизнеса — проект не спрашиваем; копия та же — have.
	f.mu.Lock()
	f.asked = nil
	f.answers["sarpo/backup?have=sarpo-2026-11-07.sql.gz"] = ""
	f.mu.Unlock()
	if err := a.SyncFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	asked := append([]string(nil), f.asked...)
	f.mu.Unlock()
	if len(asked) != 2 || asked[0] != "sarpo/backup?have=sarpo-2026-11-07.sql.gz" || asked[1] != "sarpo/backups?" {
		t.Fatalf("спрошено: %v", asked)
	}
	// «Программы» — раз в день; счёт дня копится, в новый день — с нуля.
	f.mu.Lock()
	progAsked := f.progAsked
	f.mu.Unlock()
	if progAsked != 1 {
		t.Fatal("«Программы» спрошены не раз в день:", progAsked)
	}
	if td := a.Cfg.Get().Today; td == nil || td.Day != "2026-11-08" || td.Programs != 2 || td.Projects["sarpo"].Files < 2 {
		t.Fatalf("счёт дня: %+v", td)
	}
	now = now.Add(24 * time.Hour)
	a.countToday("sarpo", &files.Result{Files: 1})
	if td := a.Cfg.Get().Today; td.Day != "2026-11-09" || td.Programs != 0 || td.Projects["sarpo"].Files != 1 {
		t.Fatalf("новый день: %+v", td)
	}
}

func TestFilesDue(t *testing.T) {
	day := time.Date(2026, 11, 8, 7, 0, 0, 0, time.UTC)    // 12:00 в Ташкенте
	night := time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC) // 01:00
	if !FilesDue(time.Time{}, day) || FilesDue(day.Add(-29*time.Minute), day) || !FilesDue(day.Add(-30*time.Minute), day) {
		t.Fatal("днём — раз в 30 минут")
	}
	if FilesDue(night.Add(-2*time.Hour), night) || !FilesDue(night.Add(-3*time.Hour), night) {
		t.Fatal("ночью — раз в 3 часа")
	}
}

func TestSyncFilesRoot(t *testing.T) {
	// У Strassio один «бизнес»: файлы прямо в папке «Strassio», договоры — PDF.
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{
		projects: `{"projects":[{"slug":"strassio","folder":"Strassio","copy":false,"ok":true,"businesses":[{"id":1,"slug":"strassio","name":"Strassio","kind":"plugin","full":true,"root":true,"ver":"v1","files":[{"key":"keys","folder":"","name":"Ключи.xlsx","refresh":true}]}]}]}`,
		answers: map[string]string{
			"strassio/excel?b=1&f=keys":            "keys",
			"strassio/items?arch=1&b=1":            `{"groups":[{"kind":"client","folder":"Клиенты","archive":"Клиенты (архив)","files":["Всё о клиенте.xlsx"],"items":[{"id":5,"dir":"Ахмед","ver":"1","photos":[{"key":"c9","file":"Договор №1","ver":"9"}]}]}]}`,
			"strassio/items?b=1&ids=5&kind=client": `{"items":[{"id":5,"files":[{"name":"Всё о клиенте.xlsx","data":"eA=="}]}],"more":[]}`,
			"strassio/photo?b=1&c=9&link=1&p=5":    "%PDF-1.4",
		},
	}
	a, disk := newApp(t, f, &now)
	if err := a.SyncFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(disk, "Little Hub", "Strassio")
	if b, _ := os.ReadFile(filepath.Join(root, "Ключи.xlsx")); string(b) != "keys" {
		t.Fatal("нет Ключи.xlsx в папке Strassio")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Клиенты", "Ахмед", "Всё о клиенте.xlsx")); string(b) != "x" {
		t.Fatal("нет «Всё о клиенте»")
	}
	if _, err := os.Stat(filepath.Join(root, "strassio")); err == nil {
		t.Fatal("лишняя папка бизнеса")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Клиенты", "Ахмед", "Договор №1.pdf")); string(b) != "%PDF-1.4" {
		t.Fatal("нет договора PDF")
	}
}

func TestUpdateAskFromHub(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`, ask: "40", latest: "1.0.0"}
	a, _ := newApp(t, f, &now)
	a.Exe = filepath.Join(t.TempDir(), "LittleHub.exe")
	checks := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.checks
	}
	wait := func(n int) {
		t.Helper()
		for i := 0; i < 200 && (checks() < n || a.View().Updating); i++ {
			time.Sleep(10 * time.Millisecond)
		}
	}
	ctx := context.Background()
	// Просьба была до запуска — не в счёт.
	a.tick(ctx)
	time.Sleep(30 * time.Millisecond)
	if checks() != 0 {
		t.Fatal("старая просьба", checks())
	}
	// Новая — проверить обновление сейчас; новой версии нет — так и пишем.
	f.mu.Lock()
	f.ask = "41"
	f.mu.Unlock()
	now = now.Add(2 * time.Minute)
	a.tick(ctx)
	wait(1)
	if checks() != 1 {
		t.Fatal("не проверил", checks())
	}
	if !hasLog(a, "Хаб попросил обновить") || !hasLog(a, "у вас последняя (1.0.0)") {
		t.Fatal(a.View().Log)
	}
	// Та же просьба — ещё раз не проверяем.
	now = now.Add(2 * time.Minute)
	a.tick(ctx)
	time.Sleep(30 * time.Millisecond)
	if checks() != 1 {
		t.Fatal("повтор", checks())
	}
	// Кнопка в окне: есть новая версия, но файла для этой системы нет — ошибка видна в окне.
	f.mu.Lock()
	f.latest = "9.0.0"
	f.mu.Unlock()
	a.UpdateNow()
	wait(2)
	if v := a.View(); v.Latest != "9.0.0" || !strings.Contains(v.UpdateErr, "нет файла") || v.Updating {
		t.Fatal(v.Latest, v.UpdateErr, v.Updating)
	}
}

func TestUpdateNowNotInstalled(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`, latest: "9.0.0"}
	a, _ := newApp(t, f, &now)
	a.UpdateNow()
	if !hasLog(a, "не с места установки") || f.checks != 0 {
		t.Fatal(a.View().Log)
	}
	if a.seeUpdateAsk("") {
		t.Fatal("пустая просьба")
	}
}

func TestFolderOf(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`}
	a, disk := newApp(t, f, &now)
	dir, err := a.FolderOf("")
	if err != nil || dir != filepath.Join(disk, "Little Hub") {
		t.Fatal(dir, err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatal("папки нет", err)
	}
	if a.View().Folder != dir {
		t.Fatal(a.View().Folder)
	}
	if d, err := a.FolderOf(disk); err != nil || d != dir {
		t.Fatal(d, err)
	}
	if _, err := a.FolderOf(t.TempDir()); err == nil {
		t.Fatal("чужой путь открылся")
	}
}

func hasLog(a *App, s string) bool {
	for _, l := range a.View().Log {
		if strings.Contains(l.Text, s) {
			return true
		}
	}
	return false
}
