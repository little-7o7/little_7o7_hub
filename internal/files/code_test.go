package files

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// «Код» (пункты 89–90): архив — после нового push, один на день, 30 дней (последний — всегда); Excel — раз в день
// и после push; всё под паролем копий; чужие имена — мимо; пропавший репозиторий — папка остаётся.

type fakeCode struct {
	repos []hub.CodeRepo
	err   error
	got   []string
	fail  map[string]error
}

func (f *fakeCode) Code(context.Context) ([]hub.CodeRepo, error) { return f.repos, f.err }
func (f *fakeCode) CodeFile(_ context.Context, repo, what string, w io.Writer, _ int64) (hub.Saved, error) {
	f.got = append(f.got, repo+"/"+what)
	if err := f.fail[repo+"/"+what]; err != nil {
		return hub.Saved{}, err
	}
	n, err := io.WriteString(w, what+" of "+repo)
	return hub.Saved{Bytes: int64(n)}, err
}

func TestCode(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, days.Tashkent)
	d := &Disk{Root: root, Today: days.Today(now), Lock: testLock(t)}
	f := &fakeCode{
		repos: []hub.CodeRepo{
			{Name: "sarpobusiness", Branch: "master", PushedAt: "2026-11-08T05:00:00Z"},
			{Name: "little_7o7", Branch: "main", PushedAt: "2026-11-08T04:00:00Z"},
			{Name: "empty", Branch: "main"},
			{Name: "..", Branch: "main"},
			{Name: ".hidden", Branch: "main"},
			{Name: "SARPOBUSINESS", Branch: "main"}, // тёзка без учёта регистра
		},
		fail: map[string]error{"empty/zip": hub.ErrNothing, "empty/excel": hub.ErrNothing},
	}
	res, err := Code(ctx, f, d, now)
	if err != nil || res.Repos != 3 || res.Zips != 2 || res.Excels != 2 || len(res.Errors) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	dir := filepath.Join(root, CodeFolder)
	for p, want := range map[string]string{
		"sarpobusiness/sarpobusiness-2026-11-08.zip": "zip of sarpobusiness",
		"sarpobusiness/sarpobusiness.xlsx":           "excel of sarpobusiness",
		"little_7o7/little_7o7-2026-11-08.zip":       "zip of little_7o7",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if exists(full) || opened(t, full+fileseal.Ext) != want {
			t.Fatal("не под паролем или не то:", p)
		}
	}
	if exists(filepath.Join(root, "..", "x")) || exists(filepath.Join(dir, ".hidden")) {
		t.Fatal("чужое имя")
	}

	// Тот же push в тот же день — ничего не качаем.
	f.got = nil
	if res, err = Code(ctx, f, d, now.Add(time.Hour)); err != nil || res.Zips+res.Excels != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Join(f.got, ",") != "" {
		t.Fatal("скачано заново:", f.got)
	}

	// Новый день: Excel — заново, архив — только у того, где был push. Архив удалили руками — скачать снова.
	next := now.AddDate(0, 0, 1)
	d.Today = days.Today(next)
	f.repos[0].PushedAt = "2026-11-09T03:00:00Z"
	_ = os.Remove(filepath.Join(dir, "little_7o7", "little_7o7-2026-11-08.zip"+fileseal.Ext))
	f.got = nil
	f.fail["little_7o7/excel"] = &hub.APIError{Status: 502, Msg: "GitHub не ответил"}
	res, err = Code(ctx, f, d, next)
	if err != nil || res.Zips != 2 || res.Excels != 1 || len(res.Errors) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if !exists(filepath.Join(dir, "sarpobusiness", "sarpobusiness-2026-11-08.zip"+fileseal.Ext)) || !exists(filepath.Join(dir, "sarpobusiness", "sarpobusiness-2026-11-09.zip"+fileseal.Ext)) {
		t.Fatal("архивы по дням")
	}
	if strings.Join(f.got, ",") != "sarpobusiness/zip,sarpobusiness/excel,little_7o7/zip,little_7o7/excel,empty/excel" {
		t.Fatal("в новый день:", f.got)
	}

	// Через месяц: старые архивы убраны, последний остаётся; репозиторий пропал с GitHub — папка на месте.
	later := now.AddDate(0, 0, 40)
	d.Today = days.Today(later)
	f.repos = f.repos[:1]
	f.fail = map[string]error{}
	if _, err := Code(ctx, f, d, later); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "sarpobusiness", "sarpobusiness-2026-11-08.zip"+fileseal.Ext)) || !exists(filepath.Join(dir, "sarpobusiness", "sarpobusiness-2026-11-09.zip"+fileseal.Ext)) {
		t.Fatal("старые архивы: последний должен остаться, остальные — уйти")
	}
	if !exists(filepath.Join(dir, "little_7o7")) {
		t.Fatal("папка пропавшего репозитория удалена")
	}

	// Ключа GitHub нет или хаб старше — ничего; нет связи — ошибка.
	for _, e := range []error{nil, hub.ErrNothing} {
		if res, err := Code(ctx, &fakeCode{err: e}, d, later); err != nil || res.Repos != 0 {
			t.Fatal(res, err)
		}
	}
	if _, err := Code(ctx, &fakeCode{err: errors.New("нет связи")}, d, later); err == nil {
		t.Fatal("ошибка потерялась")
	}
	// Нет связи посреди раскладки — стоп.
	g := &fakeCode{repos: []hub.CodeRepo{{Name: "a", PushedAt: "x"}, {Name: "b", PushedAt: "y"}}, fail: map[string]error{"a/zip": errors.New("связь оборвалась")}}
	if _, err := Code(ctx, g, d, later); err == nil || strings.Contains(strings.Join(g.got, ","), "b/") {
		t.Fatal("после обрыва связи продолжил", err, g.got)
	}
}
