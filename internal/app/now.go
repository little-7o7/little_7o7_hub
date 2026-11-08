package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/files"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// «Сразу на компьютерах» (план little_7o7/.claude/plans/pc-backup-2.plan.md, решения 1–5; владелец 2026-11-08: «v sayte
// yest odna prodaja, srazu otpravlyat v kompyuter. izmenit exel fayli, zamenit backup, srazu», «2-3 daje i 5 normalno»,
// «dlya vsex proyektov srazu»). У проекта свой флаг изменений (Sarpo: /api/pc-flag/<ключ> в CDN проекта, базу не
// будит) — программа смотрит его раз в 30 секунд; время во флаге сменилось — через 30 секунд затишья (не позже
// 3 минут) раскладывает только этот проект: Excel и папки, где сменилась версия, и копию базы «за сегодня». У проекта,
// чьи файлы собирает хаб (Strassio), флаг — флаг хаба. Ещё: докачка пропущенных дней (копии хаба и ночные копии
// проектов), месяц хранения копий, место на диске по проектам.

const (
	ProjectFlagEvery = 30 * time.Second
	ChangeQuiet      = 30 * time.Second
	ChangeMax        = 3 * time.Minute
	StepEvery        = 20 * time.Second
	// KeepCopies — сколько дней хранить копии баз (владелец: «1 mesyats»).
	KeepCopies = 30
	usageEvery = 6 * time.Hour
)

// projWatch — флаг изменений одного проекта.
type projWatch struct {
	url    string // адрес флага у проекта; пусто — флага нет (local — флаг хаба)
	local  bool
	copy   bool
	folder string
	seen   string // последнее значение флага
	t      int64  // время из флага проекта, мс (для копии «за сегодня»)
	synced string // значение флага, при котором проект разложен
	deb    Debounce
}

// learn — что смотреть из ответа хаба; ответ — значения флагов на начало раскладки (по ним отметить «разложен»).
func (a *App) learn(list []hub.FileProject) map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.watch == nil {
		a.watch = map[string]*projWatch{}
	}
	marks := map[string]string{}
	for _, p := range list {
		if !files.SlugRe.MatchString(p.Slug) || !p.OK {
			continue
		}
		w := a.watch[p.Slug]
		if w == nil {
			w = &projWatch{deb: Debounce{Quiet: ChangeQuiet, Max: ChangeMax}}
			a.watch[p.Slug] = w
		}
		w.url, w.local, w.copy, w.folder = p.Flag, p.Local, p.Copy, p.Folder
		if w.local {
			w.seen = a.flag
		}
		marks[p.Slug] = w.seen
	}
	return marks
}

// synced — проект разложен при значении флага v; fresh=false — нет (копия «за сегодня» старше записей): ещё раз.
func (a *App) synced(slug, v string, fresh bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if w := a.watch[slug]; w != nil && fresh {
		w.synced = v
		if w.seen == v {
			w.deb.Reset()
		}
	}
}

// flagTime — время из флага проекта (0 — не знаем).
func (a *App) flagTime(slug string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if w := a.watch[slug]; w != nil {
		return w.t
	}
	return 0
}

// pollProjects — раз в 30 секунд: флаги проектов; изменился и затих — разложить этот проект.
func (a *App) pollProjects(ctx context.Context) []string {
	if !a.Paired() {
		return nil
	}
	c, err := a.client()
	if err != nil {
		return nil
	}
	type ask struct{ slug, url string }
	var asks []ask
	a.mu.Lock()
	for slug, w := range a.watch {
		if w.url != "" {
			asks = append(asks, ask{slug, w.url})
		}
	}
	a.mu.Unlock()
	for _, q := range asks {
		t, err := c.ProjectFlag(ctx, q.url)
		a.mu.Lock()
		if w := a.watch[q.slug]; w != nil && w.url == q.url {
			switch {
			case err == nil:
				w.seen, w.t = strconv.FormatInt(t, 10), t
			case hub.IsAPI(err):
				w.url = "" // адрес устарел (ключи проекта сменили) — новый придёт с раскладкой всех проектов
			}
		}
		a.mu.Unlock()
	}
	now := a.now()
	var due []string
	a.mu.Lock()
	for slug, w := range a.watch {
		if w.deb.Observe(w.seen, w.synced, now) {
			due = append(due, slug)
		}
	}
	a.mu.Unlock()
	return due
}

// syncDue — разложить проекты, чей флаг изменился (по одному; идёт другая раскладка — в следующий раз).
func (a *App) syncDue(ctx context.Context, due []string) {
	for _, slug := range due {
		if ran, err := a.runFiles(ctx, slug, false); !ran || (err != nil && files.Fatal(err)) {
			return
		}
	}
}

// seeSyncAsk — просьба «Сохранить сейчас на всех» из хаба: новая (не та, что была при запуске) — разложить всё.
func (a *App) seeSyncAsk(s string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s == "" {
		return false
	}
	ask := a.syncSeenOK && s != a.syncSeen
	a.syncSeen, a.syncSeenOK = s, true
	return ask
}

// SaveAllNow — копия хаба и все файлы и копии проектов заново, с проверкой всего (кнопка в хабе).
func (a *App) SaveAllNow(ctx context.Context) {
	if !a.Paired() {
		return
	}
	if a.CopyHub(ctx) == nil {
		a.SendStatus(ctx)
	}
	_, _ = a.runFiles(ctx, "", true)
}

// localChanged — флаг хаба сменился: у проектов, чьи файлы собирает хаб, — тоже.
func (a *App) localChanged(v string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, w := range a.watch {
		if w.local {
			w.seen = v
		}
	}
}

// ---------- Копия «за сегодня» ----------

const livePrefix = "live:"

func liveMade(version string) int64 {
	if !strings.HasPrefix(version, livePrefix) {
		return 0
	}
	n, _ := strconv.ParseInt(version[len(livePrefix):], 10, 64)
	return n
}

// liveCopy — копия базы «за сегодня»: лежит и не старше записей из флага — не спрашиваем; иначе — у проекта (он отдаёт
// ту же выгрузку всем компьютерам, пока записей не было).
func (a *App) liveCopy(ctx context.Context, src files.Source, p hub.FileProject, root string, res *files.Result) (bool, error) {
	proj := days.Project{Slug: p.Slug, Folder: p.Folder, Ext: "l7copy"}
	cfg := a.Cfg.Get()
	prev := cfg.Copies[p.Slug]
	now := a.now()
	today := days.Today(now)
	t := a.flagTime(p.Slug)
	made := int64(0)
	if rel, err := days.Rel(proj, today); err == nil && prev.Day == today && days.Exists(filepath.Join(root, rel)) {
		made = liveMade(prev.Version)
	}
	if made > 0 && t > 0 && made >= t {
		return true, nil
	}
	a.step(p.Folder + ": копия базы за сегодня")
	cp, err := files.LiveCopy(ctx, src, proj, root, t, made)
	switch {
	case errors.Is(err, hub.ErrNothing):
		return t == 0 || made >= t, nil
	case errors.Is(err, hub.ErrNoPassword):
		res.Fail("Копия базы", err)
		return true, nil
	case err != nil && files.Fatal(err):
		return false, err
	case err != nil:
		res.Fail("Копия базы", err)
		return true, nil
	}
	version := cp.Name
	if cp.Made > 0 {
		version = livePrefix + strconv.FormatInt(cp.Made, 10)
	}
	if err := a.Cfg.Update(func(x *config.Config) {
		x.Copies[p.Slug] = config.Copy{Day: cp.Day, At: now.UTC().Format(time.RFC3339), Bytes: cp.Bytes, Version: version, Disk: filepath.Dir(root)}
	}); err != nil {
		return false, err
	}
	if prev.Day != cp.Day {
		a.Logf(false, "Копия базы %s за %s: %s", p.Folder, cp.Day, cp.Path)
	}
	return t == 0 || cp.Made >= t, nil
}

// ---------- Пропущенные дни ----------

// catchUpProject — ночные копии проекта за дни, которых на диске нет (компьютер был выключен в полночь), за месяц.
func (a *App) catchUpProject(ctx context.Context, src files.Source, p hub.FileProject, root string, res *files.Result) error {
	var r struct {
		Copies []struct {
			Name string `json:"name"`
		} `json:"copies"`
	}
	if err := src.JSON(ctx, "backups", nil, &r); err != nil {
		if files.Fatal(err) {
			return err
		}
		return nil // проект старше (списка нет) — не беда
	}
	proj := days.Project{Slug: p.Slug, Folder: p.Folder, Ext: "l7copy"}
	now := a.now()
	today, oldest := days.Today(now), days.Since(now, KeepCopies)
	have := map[string]bool{}
	for _, d := range days.List(root, proj) {
		have[d.Day] = true
	}
	n := 0
	for _, c := range r.Copies {
		d := files.NightName(c.Name)
		if d == "" || d >= today || d < oldest || have[d] {
			continue
		}
		a.step(fmt.Sprintf("%s: копия базы за %s", p.Folder, d))
		_, err := files.NightCopy(ctx, src, proj, root, c.Name, d)
		switch {
		case err != nil && files.Fatal(err):
			return err
		case err != nil:
			res.Fail("Копия базы за "+d, err)
			continue
		}
		have[d] = true
		n++
	}
	if n > 0 {
		a.Logf(false, "%s: докачаны копии базы за пропущенные дни: %d", p.Folder, n)
	}
	return nil
}

// catchUpHub — копии хаба за дни, которых на диске нет: хаб хранит каждый прошедший день месяц.
func (a *App) catchUpHub(ctx context.Context, c *hub.Client, root string) {
	list, err := c.HubCopies(ctx)
	if err != nil {
		return // хаб старше — списка нет
	}
	now := a.now()
	today, oldest := days.Today(now), days.Since(now, KeepCopies)
	have := map[string]bool{}
	for _, d := range days.List(root, HubProject) {
		have[d.Day] = true
	}
	n := 0
	for _, dc := range list {
		if !days.ValidDay(dc.Day) || dc.Day >= today || dc.Day < oldest || have[dc.Day] {
			continue
		}
		a.step("Хаб: копия за " + dc.Day)
		cp, err := c.HubCopyDay(ctx, dc.Day)
		if err != nil {
			continue
		}
		rel, err := days.Rel(HubProject, dc.Day)
		if err != nil || copyfmt.Check(cp.Data) != nil {
			continue
		}
		if err := days.Put(filepath.Join(root, rel), cp.Data, cp.Sha256); err == nil {
			n++
		}
	}
	if n > 0 {
		a.Logf(false, "Хаб: докачаны копии за пропущенные дни: %d", n)
	}
}

// ---------- Месяц хранения и место на диске ----------

// copyProjects — чьи копии баз лежат на диске: хаб и проекты с копией.
func copyProjects(list []hub.FileProject) []days.Project {
	out := []days.Project{HubProject}
	for _, p := range list {
		if p.Copy && files.SlugRe.MatchString(p.Slug) && files.Ok(p.Folder) {
			out = append(out, days.Project{Slug: p.Slug, Folder: p.Folder, Ext: "l7copy"})
		}
	}
	return out
}

// prune — копии баз старше месяца удалить (пункт 107).
func (a *App) prune(root string, list []hub.FileProject, disk *files.Disk) {
	n := 0
	for _, p := range copyProjects(list) {
		n += days.Prune(root, p, a.now(), KeepCopies)
	}
	if n > 0 {
		a.Logf(false, "Удалены копии баз старше %d дней: %d", KeepCopies, n)
	}
	// «Старые версии» Excel — тоже месяц (пункт 108); записанные открытыми до пароля копий — зашифровать.
	if n := files.PruneOld(root, a.now(), KeepCopies); n > 0 {
		a.Logf(false, "Удалены старые версии Excel старше %d дней: %d дн.", KeepCopies, n)
	}
	files.SealAll(filepath.Join(root, files.OldFolder), disk)
}

// measure — сколько занимает каждый проект на диске (пункт 115), не чаще раза в 6 часов.
func (a *App) measure(root string, list []hub.FileProject) {
	a.mu.Lock()
	due := a.usage == nil || a.now().Sub(a.usageAt) >= usageEvery
	a.mu.Unlock()
	if !due {
		return
	}
	folders := map[string]string{HubProject.Slug: HubProject.Folder, "old": files.OldFolder, "programs": files.ProgramsFolder}
	for _, p := range list {
		if files.SlugRe.MatchString(p.Slug) && files.Ok(p.Folder) {
			folders[p.Slug] = p.Folder
		}
	}
	usage := map[string]hub.Usage{}
	for slug, folder := range folders {
		var u hub.Usage
		_ = filepath.WalkDir(filepath.Join(root, folder), func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") { // служебные файлы программы не в счёт
				return nil
			}
			if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
				u.Bytes += info.Size()
				u.Files++
			}
			return nil
		})
		if u.Files > 0 {
			usage[slug] = u
		}
	}
	a.mu.Lock()
	a.usage, a.usageAt = usage, a.now()
	a.mu.Unlock()
}

// copyDays — дни копий баз на диске (30 последних) по проектам: хаб сравнивает по ним компьютеры (пункт 112).
func (a *App) copyDays(root string) map[string][]string {
	ps := []days.Project{HubProject}
	a.mu.Lock()
	for slug, w := range a.watch {
		if w.copy && files.Ok(w.folder) {
			ps = append(ps, days.Project{Slug: slug, Folder: w.folder, Ext: "l7copy"})
		}
	}
	a.mu.Unlock()
	out := map[string][]string{}
	for _, p := range ps {
		list := days.List(root, p)
		if len(list) > KeepCopies+1 {
			list = list[len(list)-KeepCopies-1:]
		}
		ds := make([]string, 0, len(list))
		for _, d := range list {
			ds = append(ds, d.Day)
		}
		if len(ds) > 0 {
			out[p.Slug] = ds
		}
	}
	return out
}
