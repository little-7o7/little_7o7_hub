// Package app — работа программы Little Hub: подключение к хабу, копия базы хаба на диски по дням, курсы валют,
// состояние для хаба и окна, автообновление (план little_7o7/.claude/plans/pc-backup-1.plan.md).
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/files"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
	"github.com/little-7o7/little_7o7_hub/internal/rates"
	"github.com/little-7o7/little_7o7_hub/internal/secret"
	"github.com/little-7o7/little_7o7_hub/internal/update"
)

// HubProject — копия базы самого хаба. Копии и файлы проектов (Sarpo) — SyncFiles.
var HubProject = days.Project{Slug: "hub", Folder: "Хаб", Ext: "l7copy"}

const (
	// Файлы проектов: днём (8:00–22:00 по Ташкенту) — раз в 30 минут, ночью — раз в 3 часа; бизнес, где ничего не
	// менялось, проект не спрашиваем.
	FilesDay     = 30 * time.Minute
	FilesNight   = 3 * time.Hour
	filesCheck   = 5 * time.Minute
	FlagEvery    = time.Minute
	UpdateEvery  = 6 * time.Hour
	StatusEvery  = 3 * time.Hour
	retryMissing = 30 * time.Minute
)

// App — одна программа на компьютер.
type App struct {
	Version string
	Dir     string
	Cfg     *config.Store
	Secret  secret.Store
	Space   disks.SpaceFunc
	HTTP    *http.Client // для сайтов курса
	Now     func() time.Time
	// Exe — файл программы (для обновления); пусто — не обновляемся (разработка).
	Exe string
	// OnRestart — новая версия поставлена: перезапуститься.
	OnRestart func()

	mu        sync.Mutex
	deb       Debounce
	busy      bool
	lastTry   time.Time
	lastErr   string
	revoked   bool
	noPass    bool
	flag      string
	copied    string
	ratesRes  []hub.RateResult
	ratesErr  []string
	latest    string
	updateErr string
	log       []LogLine
	kick      chan struct{}

	filesBusy bool
	filesTry  time.Time
	filesStep string
	stepAt    time.Time
	stepSent  time.Time

	// Флаги изменений проектов (now.go): что смотреть и при каком значении проект разложен.
	watch   map[string]*projWatch
	usage   map[string]hub.Usage
	usageAt time.Time
	// «Сохранить сейчас на всех» из хаба: последняя виденная просьба (как updSeen).
	syncSeen   string
	syncSeenOK bool

	// Обновление: updBusy — идёт проверка или установка; updSeen — просьба «Обновить программу» из хаба, которую уже
	// видели (пусто — флаг ещё не читали: старая просьба до запуска программы не в счёт).
	updBusy   bool
	updSeen   string
	updSeenOK bool
}

type LogLine struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
	Bad  bool      `json:"bad,omitempty"`
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Logf — строка журнала окна (последние 200) и файла littlehub.log.
func (a *App) Logf(bad bool, format string, args ...any) {
	line := LogLine{At: a.now(), Text: fmt.Sprintf(format, args...), Bad: bad}
	a.mu.Lock()
	a.log = append(a.log, line)
	if len(a.log) > 200 {
		a.log = a.log[len(a.log)-200:]
	}
	a.mu.Unlock()
	if a.Dir != "" {
		if f, err := os.OpenFile(filepath.Join(a.Dir, "littlehub.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			mark := " "
			if bad {
				mark = "!"
			}
			fmt.Fprintf(f, "%s %s %s\n", line.At.Format(time.RFC3339), mark, line.Text)
			_ = f.Close()
		}
	}
}

// TrimLog — журнал-файл больше 2 МБ — начинаем новый (старый — littlehub.log.1).
func TrimLog(dir string) {
	p := filepath.Join(dir, "littlehub.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(p, p+".1")
	}
}

func (a *App) client() (*hub.Client, error) {
	c := a.Cfg.Get()
	if c.Hub == "" {
		return nil, hub.ErrRevoked
	}
	tok, err := a.Secret.Get()
	if err != nil {
		if errors.Is(err, secret.ErrNone) {
			return nil, hub.ErrRevoked
		}
		return nil, err
	}
	return &hub.Client{Base: c.Hub, Token: tok, Version: a.Version}, nil
}

// Paired — подключён ли компьютер.
func (a *App) Paired() bool {
	_, err := a.client()
	a.mu.Lock()
	defer a.mu.Unlock()
	return err == nil && !a.revoked
}

// Pair — код из Настроек → Компьютеры (окно программы или команда установки).
func (a *App) Pair(ctx context.Context, base, code string) (hub.Paired, error) {
	if base == "" {
		base = a.Cfg.Get().Hub
	}
	b, err := hub.CheckBase(base)
	if err != nil {
		return hub.Paired{}, err
	}
	code = strings.ToUpper(strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			return r
		}
		return -1
	}, code))
	if len(code) != 16 {
		return hub.Paired{}, hub.ErrBadCode
	}
	c := &hub.Client{Base: b, Version: a.Version}
	p, err := c.Pair(ctx, code, ComputerName())
	if err != nil {
		return p, err
	}
	if err := a.Secret.Set(p.Token); err != nil {
		return p, fmt.Errorf("ключ не сохранился: %w", err)
	}
	err = a.Cfg.Update(func(cfg *config.Config) {
		cfg.Hub, cfg.Flag, cfg.DeviceID, cfg.Name = b, p.Flag, p.ID, p.Name
		if len(cfg.Disks) == 0 {
			cfg.Disks = []string{disks.Default()}
		}
	})
	a.mu.Lock()
	a.revoked, a.noPass, a.lastErr = false, false, ""
	a.mu.Unlock()
	a.Logf(false, "Подключён к хабу как «%s»", p.Name)
	a.Kick()
	return p, err
}

// ComputerName — имя компьютера для хаба: «OFFICE-PC», «MacBook-Pro».
func ComputerName() string {
	n, err := os.Hostname()
	if err != nil || n == "" {
		n = runtime.GOOS
	}
	n = strings.TrimSuffix(strings.TrimSuffix(n, ".local"), ".lan")
	if len(n) > 80 {
		n = n[:80]
	}
	return n
}

func (a *App) setErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case err == nil:
		a.lastErr, a.noPass = "", false
	case errors.Is(err, hub.ErrRevoked):
		a.revoked, a.lastErr = true, err.Error()
	case errors.Is(err, hub.ErrNoPassword):
		a.noPass, a.lastErr = true, err.Error()
	default:
		a.lastErr = err.Error()
	}
}

// Kick — «Сохранить сейчас»: копия без ожидания.
func (a *App) Kick() {
	a.mu.Lock()
	if a.kick == nil {
		a.kick = make(chan struct{}, 1)
	}
	k := a.kick
	a.mu.Unlock()
	select {
	case k <- struct{}{}:
	default:
	}
}

// CopyHub — взять копию базы хаба и положить в файл сегодняшнего дня.
func (a *App) CopyHub(ctx context.Context) error {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return nil
	}
	a.busy, a.lastTry = true, a.now()
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.busy = false
		a.mu.Unlock()
	}()
	err := a.copyHub(ctx)
	a.setErr(err)
	if err != nil {
		a.Logf(true, "Копия хаба не сохранена: %v", err)
	}
	return err
}

func (a *App) copyHub(ctx context.Context) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	cp, err := c.HubCopy(ctx)
	if err != nil {
		return err
	}
	now := a.now()
	if !days.Near(cp.Day, now) {
		return fmt.Errorf("хаб прислал копию за %s, а у компьютера %s — проверьте часы", cp.Day, days.Today(now))
	}
	if err := copyfmt.Check(cp.Data); err != nil {
		return err
	}
	cfg := a.Cfg.Get()
	_, active, err := disks.Check(cfg.Disks, a.Space)
	if err != nil {
		return err
	}
	rel, err := days.Rel(HubProject, cp.Day)
	if err != nil {
		return err
	}
	path := filepath.Join(disks.Root(active), rel)
	err = days.Put(path, cp.Data, cp.Sha256)
	same := errors.Is(err, days.ErrSame)
	if err != nil && !same {
		return err
	}
	// Диск сменился посреди дня — файл этого дня на прежнем диске больше не нужен.
	for _, d := range cfg.Disks {
		if d != active {
			if old := filepath.Join(disks.Root(d), rel); days.Exists(old) {
				_ = os.Remove(old)
			}
		}
	}
	if err := a.Cfg.Update(func(x *config.Config) {
		x.Copies[HubProject.Slug] = config.Copy{Day: cp.Day, At: now.UTC().Format(time.RFC3339), Bytes: int64(len(cp.Data)), Version: cp.Version, Disk: active}
	}); err != nil {
		return err
	}
	a.mu.Lock()
	a.copied = cp.Version
	a.deb.Reset()
	a.mu.Unlock()
	if !same {
		a.Logf(false, "Копия хаба за %s сохранена: %s", cp.Day, path)
	}
	return nil
}

// FilesDue — пора ли раскладывать файлы проектов: днём раз в 30 минут, ночью раз в 3 часа.
func FilesDue(last, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	h := now.In(days.Tashkent).Hour()
	every := FilesNight
	if h >= 8 && h < 22 {
		every = FilesDay
	}
	return now.Sub(last) >= every
}

// SyncFiles — файлы всех проектов на активный диск: Excel, папки клиентов, поставщиков, мастеров, товаров с фото, и
// копия базы проекта. Уже идёт — ничего.
func (a *App) SyncFiles(ctx context.Context) error {
	_, err := a.runFiles(ctx, "", false)
	return err
}

// runFiles — раскладка всех проектов (only == "") или одного; force — проверить всё заново. Уже идёт — ran=false.
func (a *App) runFiles(ctx context.Context, only string, force bool) (ran bool, err error) {
	a.mu.Lock()
	if a.filesBusy {
		a.mu.Unlock()
		return false, nil
	}
	a.filesBusy = true
	if only == "" {
		a.filesTry = a.now()
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.filesBusy, a.filesStep = false, ""
		a.mu.Unlock()
	}()
	err = a.syncFiles(ctx, only, force)
	if err != nil {
		a.Logf(true, "Файлы проектов: %v", err)
	}
	return true, err
}

// step — что программа делает сейчас: в окно и, не чаще раза в 20 секунд, хабу (пункт 88). "" — закончила.
func (a *App) step(s string) {
	now := a.now()
	a.mu.Lock()
	a.filesStep, a.stepAt = s, now
	send := s != "" && now.Sub(a.stepSent) >= StepEvery
	if send {
		a.stepSent = now
	}
	a.mu.Unlock()
	if send {
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		a.SendStatus(sctx)
		cancel()
	}
}

func (a *App) syncFiles(ctx context.Context, only string, force bool) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	list, err := c.Projects(ctx, only)
	if err != nil {
		return err
	}
	marks := a.learn(list)
	cfg := a.Cfg.Get()
	_, active, err := disks.Check(cfg.Disks, a.Space)
	if err != nil {
		return err
	}
	root := disks.Root(active)
	now := a.now()
	today := days.Today(now)
	state := &config.Files{At: now.UTC().Format(time.RFC3339), Disk: active, Projects: map[string]config.ProjectFiles{}}
	if cfg.Files != nil && cfg.Files.Disk == active {
		for k, v := range cfg.Files.Projects {
			state.Projects[k] = v
		}
	}
	var stop error
	full := only == ""
	for _, p := range list {
		if stop != nil || !files.SlugRe.MatchString(p.Slug) || !files.Ok(p.Folder) {
			continue
		}
		res := &files.Result{Errors: []string{}}
		if !p.OK {
			// Проект не подключён или не ответил: его папку не трогаем, прежний итог оставляем с причиной.
			prev := state.Projects[p.Slug]
			prev.Errors = []string{"Проект не ответил: " + p.Error}
			state.Projects[p.Slug] = prev
			continue
		}
		src := files.Of(c, p.Slug)
		dir := filepath.Join(root, p.Folder)
		for _, b := range p.Businesses {
			if !files.SlugRe.MatchString(b.Slug) {
				continue
			}
			bdir := filepath.Join(dir, b.Slug)
			if b.Root {
				bdir = dir // один «бизнес» у проекта (Strassio) — прямо в его папке
			}
			if err := files.Business(ctx, src, b, bdir, today, force, res, a.step); err != nil {
				stop = err
				break
			}
		}
		fresh := true
		if stop == nil && p.Copy {
			fresh, stop = a.projectCopy(ctx, src, p, root, res)
		}
		if stop == nil && p.Copy && full {
			stop = a.catchUpProject(ctx, src, p, root, res)
		}
		if stop == nil {
			// Разложен при том флаге, что был на начале раскладки: запись во время неё — ещё раз. Копия «за сегодня»
			// старше записей (проект отдал выгрузку прошлой минуты) — тоже ещё раз.
			a.synced(p.Slug, marks[p.Slug], fresh)
		}
		state.Projects[p.Slug] = config.ProjectFiles{Businesses: res.Businesses, Files: res.Files, Items: res.Items, Photos: res.Photos, Locked: res.Locked, Errors: res.Errors}
		if res.Files+res.Photos > 0 || len(res.Errors) > 0 {
			a.Logf(len(res.Errors) > 0, "%s: файлов %d, папок %d, фото %d%s%s", p.Folder, res.Files, res.Items, res.Photos, lockedNote(res.Locked), errNote(res.Errors))
		}
	}
	if err := a.Cfg.Update(func(x *config.Config) { x.Files = state }); err != nil {
		return err
	}
	if stop == nil && full {
		a.catchUpHub(ctx, c, root)
		a.prune(root, list)
		a.measure(root, list)
	}
	a.step("")
	if stop == nil {
		a.SendStatus(ctx)
	}
	return stop
}

func lockedNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", открыты в Excel и не обновились: %d", n)
}

func errNote(es []string) string {
	if len(es) == 0 {
		return ""
	}
	return "; ошибки: " + strings.Join(es, "; ")
}

// projectCopy — копия базы проекта в «<проект>/Копии базы/…» под паролем копий: у проекта с копией «за сегодня»
// (Sarpo с 2026-11-08) — она (liveCopy), иначе ночная. fresh=false — копия старше последних записей: попробовать ещё.
func (a *App) projectCopy(ctx context.Context, src files.Source, p hub.FileProject, root string, res *files.Result) (fresh bool, err error) {
	if p.Live {
		return a.liveCopy(ctx, src, p, root, res)
	}
	return true, a.nightCopy(ctx, src, p, root, res)
}

func (a *App) nightCopy(ctx context.Context, src files.Source, p hub.FileProject, root string, res *files.Result) error {
	proj := days.Project{Slug: p.Slug, Folder: p.Folder, Ext: "l7copy"}
	cfg := a.Cfg.Get()
	prev := cfg.Copies[p.Slug]
	havePath := ""
	if prev.Day != "" {
		if rel, err := days.Rel(proj, prev.Day); err == nil {
			havePath = filepath.Join(root, rel)
		}
	}
	a.step(p.Folder + ": копия базы")
	cp, err := files.ProjectCopy(ctx, src, proj, root, prev.Version, havePath)
	switch {
	case errors.Is(err, hub.ErrNothing):
		return nil
	case errors.Is(err, hub.ErrNoPassword):
		res.Fail("Копия базы", err)
		return nil
	case err != nil && files.Fatal(err):
		return err
	case err != nil:
		res.Fail("Копия базы", err)
		return nil
	}
	now := a.now()
	if err := a.Cfg.Update(func(x *config.Config) {
		x.Copies[p.Slug] = config.Copy{Day: cp.Day, At: now.UTC().Format(time.RFC3339), Bytes: cp.Bytes, Version: cp.Name, Disk: filepath.Dir(root)}
	}); err != nil {
		return err
	}
	a.Logf(false, "Копия базы %s за %s сохранена: %s", p.Folder, cp.Day, cp.Path)
	return nil
}

// goFiles — файлы проектов в фоне: копия хаба и курсы не ждут, пока скачиваются папки.
func (a *App) goFiles(ctx context.Context) {
	if !a.Paired() {
		return
	}
	go func() { _ = a.SyncFiles(ctx) }()
}

// Status — что отправить хабу о себе.
func (a *App) Status() hub.Status {
	cfg := a.Cfg.Get()
	st, _, derr := disks.Check(cfg.Disks, a.Space)
	copies := map[string]any{}
	for k, v := range cfg.Copies {
		copies[k] = map[string]any{"day": v.Day, "at": v.At, "bytes": v.Bytes, "version": v.Version}
	}
	a.mu.Lock()
	msg := a.lastErr
	a.mu.Unlock()
	if msg == "" && derr != nil {
		msg = derr.Error()
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	var e *string
	if msg != "" {
		e = &msg
	}
	ds := make([]map[string]any, 0, len(st))
	for _, d := range st {
		ds = append(ds, map[string]any{"path": d.Path, "free": d.Free, "total": d.Total, "active": d.Active, "ok": d.OK})
	}
	st2 := hub.Status{Version: a.Version, OS: runtime.GOOS, Disks: ds, Copies: copies, Error: e}
	if cfg.Files != nil {
		st2.Files = cfg.Files
	}
	for _, d := range st {
		if d.Active {
			st2.Days = a.copyDays(disks.Root(d.Path))
		}
	}
	a.mu.Lock()
	if a.filesBusy && a.filesStep != "" {
		st2.Work = &hub.Work{Step: truncRunes(a.filesStep, 200), At: a.stepAt.UTC().Format(time.RFC3339)}
	}
	if len(a.usage) > 0 {
		st2.Usage = a.usage
	}
	a.mu.Unlock()
	return st2
}

// SendStatus — сообщить хабу о себе (версия, диски, копии).
func (a *App) SendStatus(ctx context.Context) {
	c, err := a.client()
	if err != nil {
		return
	}
	if err := c.SendStatus(ctx, a.Status()); err != nil {
		a.setErr(err)
	}
}

// SendRates — курсы с узбекских сайтов → хаб.
func (a *App) SendRates(ctx context.Context) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	srcs, _ := c.Sources(ctx)
	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 40 * time.Second}
	}
	items, errs := rates.Collect(ctx, hc, rates.Pick(srcs))
	es := make([]string, 0, len(errs))
	for _, e := range errs {
		es = append(es, e.Error())
	}
	var res []hub.RateResult
	if len(items) > 0 {
		res, err = c.SendRates(ctx, items)
		if err != nil {
			a.setErr(err)
			a.Logf(true, "Курсы не ушли в хаб: %v", err)
			return err
		}
	}
	a.mu.Lock()
	a.ratesRes, a.ratesErr = res, es
	a.mu.Unlock()
	if len(items) > 0 {
		_ = a.Cfg.Update(func(x *config.Config) { x.RatesAt = a.now().UTC().Format(time.RFC3339) })
	}
	var ok []string
	for _, r := range res {
		if r.OK {
			ok = append(ok, r.Source)
		} else {
			es = append(es, r.Source+": "+r.Error)
		}
	}
	if len(ok) > 0 {
		a.Logf(false, "Курсы ушли в хаб: %s", strings.Join(ok, ", "))
	}
	if len(es) > 0 {
		a.Logf(true, "Курсы: %s", strings.Join(es, "; "))
	}
	return nil
}

// CheckUpdate — есть новая версия — скачать, проверить, поставить и перезапуститься. Уже идёт — ничего не делать.
func (a *App) CheckUpdate(ctx context.Context) error {
	if a.Exe == "" {
		return nil
	}
	a.mu.Lock()
	if a.updBusy {
		a.mu.Unlock()
		return nil
	}
	a.updBusy = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.updBusy = false
		a.mu.Unlock()
	}()
	c, err := a.client()
	if err != nil {
		return err
	}
	rel, err := c.Latest(ctx)
	a.mu.Lock()
	if err == nil {
		a.latest = rel.Version
	}
	a.mu.Unlock()
	if err != nil || !update.Newer(a.Version, rel.Version) {
		return err
	}
	asset, ok := rel.Assets[update.Target()]
	if !ok {
		return fmt.Errorf("в выпуске %s нет файла для %s", rel.Version, update.Target())
	}
	a.Logf(false, "Новая версия %s — скачиваю", rel.Version)
	data, err := c.Download(ctx, asset)
	if err == nil {
		err = update.Apply(a.Exe, data, func(p string) error { return update.SelfTest(p, rel.Version) })
	}
	a.mu.Lock()
	if err != nil {
		a.updateErr = err.Error()
	} else {
		a.updateErr = ""
	}
	a.mu.Unlock()
	if err != nil {
		a.Logf(true, "Обновление не поставилось: %v", err)
		return applyErr{err}
	}
	a.Logf(false, "Версия %s поставлена — перезапуск", rel.Version)
	if a.OnRestart != nil {
		a.OnRestart()
	}
	return nil
}

// applyErr — новая версия скачана, но не поставилась (уже записано в журнал).
type applyErr struct{ error }

func (e applyErr) Unwrap() error { return e.error }

// UpdateNow — «Обновить программу» (кнопка в окне или просьба из хаба): проверить и поставить сейчас, не ждать
// 6 часов. Работает в фоне; окно видит «проверяю» (View.Updating) и итог в журнале.
func (a *App) UpdateNow() {
	if a.Exe == "" {
		a.Logf(true, "Обновление: эта копия программы запущена не с места установки — обновляется только установленная")
		return
	}
	a.mu.Lock()
	if a.updBusy {
		a.mu.Unlock()
		return
	}
	a.updateErr = ""
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		a.Logf(false, "Проверяю обновление…")
		err := a.CheckUpdate(ctx)
		a.mu.Lock()
		latest := a.latest
		if err != nil && a.updateErr == "" {
			a.updateErr = err.Error()
		}
		a.mu.Unlock()
		var ae applyErr
		switch {
		case err != nil && !errors.As(err, &ae):
			a.Logf(true, "Обновление не проверилось: %v", err)
		case err == nil && !update.Newer(a.Version, latest):
			a.Logf(false, "Новой версии нет: у вас последняя (%s)", a.Version)
		}
	}()
}

// seeUpdateAsk — просьба «Обновить программу» из хаба: новая (не та, что была при запуске) — обновиться.
func (a *App) seeUpdateAsk(u string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if u == "" {
		return false
	}
	ask := a.updSeenOK && u != a.updSeen
	a.updSeen, a.updSeenOK = u, true
	return ask
}

// tick — раз в минуту: флаг хаба, и если пора — копия (30 секунд затишья, не позже 3 минут).
func (a *App) tick(ctx context.Context) {
	if !a.Paired() {
		return
	}
	cfg := a.Cfg.Get()
	now := a.now()
	a.mu.Lock()
	missing := cfg.Copies[HubProject.Slug].Day != days.Today(now) && now.Sub(a.lastTry) >= retryMissing
	noPass := a.noPass && now.Sub(a.lastTry) < retryMissing
	copied := a.copied
	a.mu.Unlock()
	if noPass {
		return
	}
	c, err := a.client()
	if err != nil {
		return
	}
	f, err := c.Flag(ctx, cfg.Flag)
	if err != nil {
		a.setErr(err)
		return
	}
	if a.seeUpdateAsk(f.U) {
		a.Logf(false, "Хаб попросил обновить программу")
		a.UpdateNow()
	}
	if a.seeSyncAsk(f.S) {
		a.Logf(false, "Хаб попросил сохранить всё сейчас")
		go a.SaveAllNow(context.WithoutCancel(ctx))
	}
	v := f.V
	a.localChanged(v)
	a.mu.Lock()
	a.flag = v
	if a.deb.Quiet == 0 {
		a.deb.Quiet, a.deb.Max = ChangeQuiet, ChangeMax
	}
	due := a.deb.Observe(v, copied, now)
	a.mu.Unlock()
	if due || missing {
		if a.CopyHub(ctx) == nil {
			a.SendStatus(ctx)
		}
	}
}

// Run — работа до выхода: копия при запуске (владелец: «pri vklyuchenii kompyutera, doljen vzyat posledniye
// izmeneniya»), флаг хаба раз в минуту, флаги проектов раз в 30 секунд, курсы раз в 3 часа, обновление раз в 6 часов.
func (a *App) Run(ctx context.Context) {
	a.Kick()
	flagT := time.NewTicker(FlagEvery)
	defer flagT.Stop()
	rateT := time.NewTicker(rates.Every)
	defer rateT.Stop()
	updT := time.NewTicker(UpdateEvery)
	defer updT.Stop()
	statT := time.NewTicker(StatusEvery)
	defer statT.Stop()
	filesT := time.NewTicker(filesCheck)
	defer filesT.Stop()
	projT := time.NewTicker(ProjectFlagEvery)
	defer projT.Stop()
	first := time.After(time.Minute)
	a.mu.Lock()
	if a.kick == nil {
		a.kick = make(chan struct{}, 1)
	}
	kick := a.kick
	a.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
			if a.Paired() && a.CopyHub(ctx) == nil {
				a.SendStatus(ctx)
			}
			a.mu.Lock()
			started := !a.filesTry.IsZero()
			a.mu.Unlock()
			// Первый запуск — файлы через минуту (вместе с курсами), дальше «Сохранить сейчас» — и файлы тоже.
			if started {
				a.goFiles(ctx)
			}
		case <-flagT.C:
			a.tick(ctx)
		case <-projT.C:
			if due := a.pollProjects(ctx); len(due) > 0 {
				go a.syncDue(ctx, due)
			}
		case <-first:
			if a.Paired() {
				_ = a.SendRates(ctx)
				a.SendStatus(ctx)
				_ = a.CheckUpdate(ctx)
				a.goFiles(ctx)
			}
		case <-filesT.C:
			a.mu.Lock()
			due := FilesDue(a.filesTry, a.now())
			a.mu.Unlock()
			if due {
				a.goFiles(ctx)
			}
		case <-rateT.C:
			if a.Paired() {
				_ = a.SendRates(ctx)
			}
		case <-statT.C:
			a.SendStatus(ctx)
		case <-updT.C:
			if a.Paired() {
				_ = a.CheckUpdate(ctx)
			}
		}
	}
}

// View — состояние для окна программы.
type View struct {
	Version   string                 `json:"version"`
	Latest    string                 `json:"latest,omitempty"`
	UpdateErr string                 `json:"updateError,omitempty"`
	Paired    bool                   `json:"paired"`
	Revoked   bool                   `json:"revoked"`
	NoPass    bool                   `json:"noPassword"`
	Hub       string                 `json:"hub"`
	Name      string                 `json:"name"`
	Busy      bool                   `json:"busy"`
	Waiting   string                 `json:"waiting,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Disks     []disks.State          `json:"disks"`
	DiskError string                 `json:"diskError,omitempty"`
	Copies    map[string]config.Copy `json:"copies"`
	Today     string                 `json:"today"`
	RatesAt   string                 `json:"ratesAt,omitempty"`
	Rates     []hub.RateResult       `json:"rates"`
	RatesErr  []string               `json:"ratesErrors"`
	Suggest   []string               `json:"suggest"`
	Log       []LogLine              `json:"log"`
	Lang      string                 `json:"lang"`
	Files     *config.Files          `json:"files,omitempty"`
	FilesBusy bool                   `json:"filesBusy"`
	FilesStep string                 `json:"filesStep,omitempty"`
	Updating  bool                   `json:"updating"`
	// Folder — папка «Little Hub» на диске, куда сейчас идут копии и файлы (кнопка «Открыть папку»).
	Folder string `json:"folder,omitempty"`
}

func (a *App) View() View {
	cfg := a.Cfg.Get()
	st, _, derr := disks.Check(cfg.Disks, a.Space)
	v := View{Version: a.Version, Paired: a.Paired(), Hub: cfg.Hub, Name: cfg.Name, Disks: st, Copies: cfg.Copies, Today: days.Today(a.now()), RatesAt: cfg.RatesAt, Lang: cfg.Lang, Files: cfg.Files}
	if derr != nil {
		v.DiskError = derr.Error()
	}
	for _, d := range st {
		if d.Active {
			v.Folder = disks.Root(d.Path)
		}
	}
	have := map[string]bool{}
	for _, d := range cfg.Disks {
		have[d] = true
	}
	for _, s := range disks.Suggest() {
		if !have[s] {
			v.Suggest = append(v.Suggest, s)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v.Latest, v.UpdateErr, v.Revoked, v.NoPass, v.Busy, v.Error = a.latest, a.updateErr, a.revoked, a.noPass, a.busy, a.lastErr
	v.FilesBusy, v.FilesStep, v.Updating = a.filesBusy, a.filesStep, a.updBusy
	if w := a.deb.Waiting(); !w.IsZero() {
		v.Waiting = w.UTC().Format(time.RFC3339)
	}
	v.Rates, v.RatesErr = a.ratesRes, a.ratesErr
	n := len(a.log)
	start := 0
	if n > 50 {
		start = n - 50
	}
	v.Log = append([]LogLine(nil), a.log[start:]...)
	return v
}

// SetDisks — новый список дисков (добавить, убрать, поднять выше) из окна.
func (a *App) SetDisks(list []string) error {
	seen := map[string]bool{}
	clean := make([]string, 0, len(list))
	for _, p := range list {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = disks.Clean(p)
		if !filepath.IsAbs(p) {
			return fmt.Errorf("нужен полный путь: %s", p)
		}
		if !seen[p] {
			seen[p] = true
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return errors.New("нужен хотя бы один диск")
	}
	if len(clean) > 20 {
		return errors.New("не больше 20 дисков")
	}
	if err := a.Cfg.Update(func(c *config.Config) { c.Disks = clean }); err != nil {
		return err
	}
	a.Logf(false, "Диски: %s", strings.Join(clean, ", "))
	return nil
}

// SetLang — язык окна.
// FolderOf — папка «Little Hub» на диске из списка дисков программы (пусто — активный). Чужой путь — ошибка: окно
// открывает только свои папки.
func (a *App) FolderOf(disk string) (string, error) {
	cfg := a.Cfg.Get()
	if disk == "" {
		st, _, err := disks.Check(cfg.Disks, a.Space)
		for _, d := range st {
			if d.Active {
				disk = d.Path
			}
		}
		if disk == "" {
			if err == nil {
				err = disks.ErrNoDisk
			}
			return "", err
		}
	} else if !slices.Contains(cfg.Disks, disk) {
		return "", errors.New("такого диска нет в списке")
	}
	dir := disks.Root(disk)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (a *App) SetLang(l string) error {
	if l != "ru" && l != "en" {
		return errors.New("язык — ru или en")
	}
	return a.Cfg.Update(func(c *config.Config) { c.Lang = l })
}

// truncRunes — не длиннее n знаков (хаб принимает шаг до 200).
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
