package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/days"
)

// «Сразу на компьютерах» (план pc-backup-2): флаг Sarpo → раскладка только Sarpo через 30 секунд затишья, копия базы
// «за сегодня», докачка пропущенных дней, месяц хранения, «Сохранить сейчас на всех», что делает сейчас — хабу.

func flagServer(t *testing.T, v *atomic.Int64) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("ключ компьютера ушёл проекту")
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"t": v.Load()})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/pc-flag/0123456789abcdef0123456789abcdef"
}

func sarpoProjects(flag, ver string) string {
	return `{"projects":[{"slug":"sarpo","folder":"Sarpo Business","copy":true,"ok":true,"live":true,"flag":"` + flag + `","businesses":[{"id":7,"slug":"sarpotextile","name":"Sarpo Textile","kind":"shop","full":true,"ver":"` + ver + `","files":[{"key":"state","folder":"Excel","name":"Магазин-2026-11-08.xlsx","refresh":false},{"key":"day:2026-11-08","folder":"Продажи","name":"Продажи-2026-11-08.xlsx","refresh":true}]}]}]}`
}

func liveHdr(made int64) map[string]string {
	return map[string]string{"X-Copy-Name": "sarpo-2026-11-08.sql.gz", "X-Copy-Day": "2026-11-08", "X-Copy-Made": strconv.FormatInt(made, 10)}
}

func TestSarpoChangeLiveCopyAndCatchUp(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC) // 11:00 в Ташкенте
	copyFile := string(sample(t))
	var flagT atomic.Int64
	flag := flagServer(t, &flagT)
	const m1, t1, m2 = int64(1794135900000), int64(1794135960000), int64(1794135990000)
	f := &fakeHub{
		copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`,
		projects: sarpoProjects(flag, "v1"),
		answers: map[string]string{
			"sarpo/excel?b=7&f=state":                   "state1",
			"sarpo/excel?b=7&f=day%3A2026-11-08":        "today1",
			"sarpo/items?arch=1&b=7":                    `{"groups":[]}`,
			"sarpo/backup?live=1&t=0":                   copyFile,
			"sarpo/backups?":                            `{"copies":[{"name":"sarpo-2026-11-08.sql.gz"},{"name":"sarpo-2026-11-06.sql.gz"},{"name":"sarpo-2026-09-01.sql.gz"}]}`,
			"sarpo/backup?name=sarpo-2026-11-08.sql.gz": copyFile,
			"sarpo/backup?name=sarpo-2026-11-06.sql.gz": copyFile,
		},
		hdrs: map[string]map[string]string{"sarpo/backup?live=1&t=0": liveHdr(m1)},
	}
	a, disk := newApp(t, f, &now)
	ctx := context.Background()
	if err := a.SyncFiles(ctx); err != nil {
		t.Fatal(err)
	}
	copies := filepath.Join(disk, "Little Hub", "Sarpo Business", "Копии базы")
	for _, d := range []string{"2026-11-08", "2026-11-07", "2026-11-05"} {
		if b, _ := os.ReadFile(filepath.Join(copies, d[:7], "sarpo-"+d+".l7copy")); string(b) != copyFile {
			t.Fatal("нет копии за", d)
		}
	}
	if v := a.Cfg.Get().Copies["sarpo"].Version; v != "live:"+strconv.FormatInt(m1, 10) {
		t.Fatal("версия копии", v)
	}
	// Флаг Sarpo: новое время — ждём затишья; через 30 секунд то же — раскладка только Sarpo.
	flagT.Store(t1)
	if due := a.pollProjects(ctx); len(due) != 0 {
		t.Fatal("сразу не раскладываем", due)
	}
	now = now.Add(ProjectFlagEvery)
	due := a.pollProjects(ctx)
	if !slices.Equal(due, []string{"sarpo"}) {
		t.Fatal("после затишья — Sarpo", due)
	}
	// Продажа: бизнес изменился, копия «за сегодня» старше записей — новая.
	f.mu.Lock()
	f.projects = sarpoProjects(flag, "v2")
	f.answers["sarpo/excel?b=7&f=state"] = "state2"
	f.answers["sarpo/excel?b=7&f=day%3A2026-11-08"] = "today2"
	have := "sarpo/backup?have=" + strconv.FormatInt(m1, 10) + "&live=1&t=" + strconv.FormatInt(t1, 10)
	f.answers[have] = copyFile
	f.hdrs[have] = liveHdr(m2)
	f.asked, f.only = nil, nil
	f.mu.Unlock()
	a.syncDue(ctx, due)
	f.mu.Lock()
	asked, only := append([]string(nil), f.asked...), append([]string(nil), f.only...)
	f.mu.Unlock()
	if !slices.Equal(only, []string{"sarpo"}) || !slices.Contains(asked, have) || slices.Contains(asked, "sarpo/backups?") {
		t.Fatalf("раскладка Sarpo: %v %v", only, asked)
	}
	biz := filepath.Join(disk, "Little Hub", "Sarpo Business", "sarpotextile")
	if b, _ := os.ReadFile(filepath.Join(biz, "Продажи", "Продажи-2026-11-08.xlsx")); string(b) != "today2" {
		t.Fatal("сегодняшний файл продаж не обновился", string(b))
	}
	if v := a.Cfg.Get().Copies["sarpo"].Version; v != "live:"+strconv.FormatInt(m2, 10) {
		t.Fatal("копия «за сегодня» не заменилась", v)
	}
	// Флаг тот же — больше не раскладываем; копия не старше записей — не спрашиваем.
	now = now.Add(ProjectFlagEvery)
	if due := a.pollProjects(ctx); len(due) != 0 {
		t.Fatal("лишняя раскладка", due)
	}
}

func TestHubCatchUpAndPrune(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	f := &fakeHub{copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`, projects: `{"projects":[]}`, days: []string{"2026-11-07", "2026-11-06", "2026-09-01"}}
	a, disk := newApp(t, f, &now)
	root := filepath.Join(disk, "Little Hub")
	// Старая копия (40 дней назад) и своя — 6-го уже есть.
	old, _ := days.Rel(HubProject, "2026-09-29")
	have, _ := days.Rel(HubProject, "2026-11-06")
	for _, rel := range []string{old, have} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755)
		_ = os.WriteFile(filepath.Join(root, rel), sample(t), 0o644)
	}
	ctx := context.Background()
	a.tick(ctx) // копия за сегодня
	if err := a.SyncFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if f.dayed != 1 {
		t.Fatal("докачать только 7-е", f.dayed)
	}
	got := []string{}
	for _, d := range days.List(root, HubProject) {
		got = append(got, d.Day)
	}
	if !slices.Equal(got, []string{"2026-11-06", "2026-11-07", "2026-11-08"}) {
		t.Fatal("дни на диске", got)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Join(root, old))); err == nil {
		t.Fatal("пустая папка сентября осталась")
	}
	st := a.Status()
	if !slices.Equal(st.Days["hub"], got) || st.Usage["hub"].Files != 3 || st.Work != nil {
		t.Fatalf("состояние: %+v %+v %+v", st.Days, st.Usage, st.Work)
	}
}

func TestSaveAllAskAndWork(t *testing.T) {
	now := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	var flagT atomic.Int64
	f := &fakeHub{
		copy: sample(t), day: "2026-11-08", version: "1", sources: `{}`, sync: "5",
		projects: sarpoProjects(flagServer(t, &flagT), "v1"),
		answers:  map[string]string{"sarpo/excel?b=7&f=state": "s", "sarpo/excel?b=7&f=day%3A2026-11-08": "d", "sarpo/items?arch=1&b=7": `{"groups":[]}`},
	}
	a, _ := newApp(t, f, &now)
	ctx := context.Background()
	if err := a.SyncFiles(ctx); err != nil {
		t.Fatal(err)
	}
	excel := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := 0
		for _, k := range f.asked {
			if k == "sarpo/excel?b=7&f=state" {
				n++
			}
		}
		return n
	}
	// Та же версия — обычная раскладка бизнес не спрашивает.
	_ = a.SyncFiles(ctx)
	if excel() != 1 {
		t.Fatal("лишний Excel", excel())
	}
	// Просьба была до запуска — не в счёт; новая — сохранить всё сейчас, с проверкой всего.
	a.tick(ctx)
	f.mu.Lock()
	f.sync = "6"
	f.mu.Unlock()
	now = now.Add(FlagEvery)
	a.tick(ctx)
	for i := 0; i < 300 && (excel() < 2 || a.View().FilesBusy); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if excel() != 2 || !hasLog(a, "сохранить всё сейчас") {
		t.Fatal("«Сохранить сейчас на всех»", excel())
	}
	// Хаб видел, что программа делала: шаг «копия базы за сегодня» во время раскладки.
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := false
	for _, s := range f.status {
		if w, ok := s["work"].(map[string]any); ok && w["step"] == "Sarpo Business: копия базы за сегодня" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("шаг не ушёл хабу")
	}
	if last := f.status[len(f.status)-1]; last["work"] != nil {
		t.Fatal("после раскладки — ничего не делает", last["work"])
	}
}

func TestTruncRunes(t *testing.T) {
	if truncRunes("абв", 5) != "абв" || truncRunes("абвгд", 3) != "аб…" {
		t.Fatal(truncRunes("абвгд", 3))
	}
	if liveMade("live:123") != 123 || liveMade("sarpo-2026-11-07.sql.gz") != 0 {
		t.Fatal("версия копии «за сегодня»")
	}
}
