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
	"strings"
	"sync"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
	"github.com/little-7o7/little_7o7_hub/internal/rates"
	"github.com/little-7o7/little_7o7_hub/internal/secret"
	"github.com/little-7o7/little_7o7_hub/internal/update"
)

// HubProject — копия базы самого хаба. Проекты (Sarpo, Strassio) добавятся в этапах 2 и 3.
var HubProject = days.Project{Slug: "hub", Folder: "Хаб", Ext: "l7copy"}

const (
	FlagEvery    = 2 * time.Minute
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
	return hub.Status{Version: a.Version, OS: runtime.GOOS, Disks: ds, Copies: copies, Error: e}
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

// CheckUpdate — есть новая версия — скачать, проверить, поставить и перезапуститься.
func (a *App) CheckUpdate(ctx context.Context) error {
	if a.Exe == "" {
		return nil
	}
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
		return err
	}
	a.Logf(false, "Версия %s поставлена — перезапуск", rel.Version)
	if a.OnRestart != nil {
		a.OnRestart()
	}
	return nil
}

// tick — раз в 2 минуты: флаг хаба, и если пора — копия.
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
	v, err := c.Flag(ctx, cfg.Flag)
	if err != nil {
		a.setErr(err)
		return
	}
	a.mu.Lock()
	a.flag = v
	due := a.deb.Observe(v, copied, now)
	a.mu.Unlock()
	if due || missing {
		if a.CopyHub(ctx) == nil {
			a.SendStatus(ctx)
		}
	}
}

// Run — работа до выхода: копия при запуске (владелец: «pri vklyuchenii kompyutera, doljen vzyat posledniye
// izmeneniya»), флаг раз в 2 минуты, курсы раз в 3 часа, обновление раз в 6 часов.
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
		case <-flagT.C:
			a.tick(ctx)
		case <-first:
			if a.Paired() {
				_ = a.SendRates(ctx)
				a.SendStatus(ctx)
				_ = a.CheckUpdate(ctx)
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
}

func (a *App) View() View {
	cfg := a.Cfg.Get()
	st, _, derr := disks.Check(cfg.Disks, a.Space)
	v := View{Version: a.Version, Paired: a.Paired(), Hub: cfg.Hub, Name: cfg.Name, Disks: st, Copies: cfg.Copies, Today: days.Today(a.now()), RatesAt: cfg.RatesAt, Lang: cfg.Lang}
	if derr != nil {
		v.DiskError = derr.Error()
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
func (a *App) SetLang(l string) error {
	if l != "ru" && l != "en" {
		return errors.New("язык — ru или en")
	}
	return a.Cfg.Update(func(c *config.Config) { c.Lang = l })
}
