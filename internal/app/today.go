package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/files"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// TodayFile — «Little Hub/Что сохранилось сегодня.txt» (план pc-backup-2, пункт 77): копии баз за сегодня, сколько
// файлов, папок и фото записано с 00:00 по Ташкенту, программы, ошибки последней раскладки. Заменяется после каждой
// полной раскладки, в новый день начинается заново.
const TodayFile = "Что сохранилось сегодня.txt"

// programsRetry — «Программы» с ошибкой (GitHub не ответил) — ещё раз не раньше чем через 3 часа.
const programsRetry = 3 * time.Hour

// countToday — прибавить записанное раскладкой проекта к счёту дня.
func (a *App) countToday(slug string, r *files.Result) {
	if r.Files+r.Items+r.Photos+r.Media == 0 {
		return
	}
	a.addToday(func(t *config.Today) {
		c := t.Projects[slug]
		c.Files += r.Files
		c.Items += r.Items
		c.Photos += r.Photos
		c.Media += r.Media
		t.Projects[slug] = c
	})
}

// addToday — поменять счёт дня (новый день — с нуля). Счёт копируется: Get отдаёт тот же указатель.
func (a *App) addToday(f func(*config.Today)) {
	today := days.Today(a.now())
	_ = a.Cfg.Update(func(x *config.Config) {
		t := config.Today{Day: today, Projects: map[string]config.DayCount{}}
		if x.Today != nil && x.Today.Day == today {
			t.Programs = x.Today.Programs
			t.Code = x.Today.Code
			for k, v := range x.Today.Projects {
				t.Projects[k] = v
			}
		}
		f(&t)
		x.Today = &t
	})
}

// programs — «Little Hub/Программы» раз в день (с ошибкой — через 3 часа ещё раз).
func (a *App) programs(ctx context.Context, c *hub.Client, root string) {
	cfg := a.Cfg.Get()
	today := days.Today(a.now())
	a.mu.Lock()
	due := cfg.ProgramsDay != today && a.now().Sub(a.programsTry) >= programsRetry
	if due {
		a.programsTry = a.now()
	}
	a.mu.Unlock()
	if !due {
		return
	}
	a.step(files.ProgramsFolder)
	res, err := files.Programs(ctx, c, root)
	if res.Files > 0 {
		a.addToday(func(t *config.Today) { t.Programs += res.Files })
		a.Logf(false, "Программы: записано %d (уже было %d)", res.Files, res.Have)
	}
	if err != nil || len(res.Errors) > 0 {
		msg := strings.Join(res.Errors, "; ")
		if err != nil {
			msg = err.Error()
		}
		a.Logf(true, "Программы: %s", msg)
		return
	}
	_ = a.Cfg.Update(func(x *config.Config) { x.ProgramsDay = today })
}

// codeEvery — «Код» не чаще раза в 3 часа: архив — только после нового push, Excel — раз в день и после push.
const codeEvery = 3 * time.Hour

// code — «Little Hub/Код» (пункты 89–90): репозитории владельца, архив кода и Excel под паролем копий.
func (a *App) code(ctx context.Context, c *hub.Client, disk *files.Disk) {
	cfg := a.Cfg.Get()
	last, _ := time.Parse(time.RFC3339, cfg.CodeAt)
	a.mu.Lock()
	due := a.now().Sub(last) >= codeEvery && a.now().Sub(a.codeTry) >= codeEvery
	if due {
		a.codeTry = a.now()
	}
	a.mu.Unlock()
	if !due {
		return
	}
	a.step(files.CodeFolder)
	res, err := files.Code(ctx, c, disk, a.now())
	if n := res.Zips + res.Excels; n > 0 {
		a.addToday(func(t *config.Today) { t.Code += n })
		a.Logf(false, "Код: архивов %d, Excel %d (репозиториев %d)", res.Zips, res.Excels, res.Repos)
	}
	if err != nil || len(res.Errors) > 0 {
		msg := strings.Join(res.Errors, "; ")
		if err != nil {
			msg = err.Error()
		}
		a.Logf(true, "Код: %s", msg)
		return
	}
	_ = a.Cfg.Update(func(x *config.Config) { x.CodeAt = a.now().UTC().Format(time.RFC3339) })
}

// writeToday — «Что сохранилось сегодня.txt» в корне «Little Hub».
func (a *App) writeToday(root string, list []hub.FileProject) {
	cfg := a.Cfg.Get()
	now := a.now().In(days.Tashkent)
	today := days.Today(now)
	t := config.Today{Projects: map[string]config.DayCount{}}
	if cfg.Today != nil && cfg.Today.Day == today {
		t = *cfg.Today
	}
	folders := map[string]string{HubProject.Slug: HubProject.Folder}
	order := []string{HubProject.Slug}
	copies := map[string]bool{HubProject.Slug: true}
	for _, p := range list {
		if !files.SlugRe.MatchString(p.Slug) || !files.Ok(p.Folder) {
			continue
		}
		if _, ok := folders[p.Slug]; !ok {
			order = append(order, p.Slug)
		}
		folders[p.Slug] = p.Folder
		copies[p.Slug] = copies[p.Slug] || p.Copy
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Little Hub — что сохранилось сегодня, %s\n", now.Format("02.01.2006"))
	fmt.Fprintf(&b, "Компьютер: %s · обновлено в %s · папка %s\n\n", cfg.Name, now.Format("15:04"), root)

	b.WriteString("КОПИИ БАЗ ЗА СЕГОДНЯ\n")
	for _, slug := range order {
		if !copies[slug] {
			continue
		}
		c, ok := cfg.Copies[slug]
		switch {
		case ok && c.Day == today:
			at := c.At
			if x, err := time.Parse(time.RFC3339, c.At); err == nil {
				at = x.In(days.Tashkent).Format("15:04")
			}
			fmt.Fprintf(&b, "• %s — сохранена в %s, %s\n", folders[slug], at, humanBytes(c.Bytes))
		case ok && c.Day != "":
			fmt.Fprintf(&b, "• %s — за сегодня ещё нет (последняя за %s)\n", folders[slug], c.Day)
		default:
			fmt.Fprintf(&b, "• %s — ещё нет\n", folders[slug])
		}
	}

	b.WriteString("\nЗАПИСАНО С 00:00 (Excel, папки, фото)\n")
	wrote := false
	for _, slug := range order {
		c, ok := t.Projects[slug]
		if !ok {
			continue
		}
		wrote = true
		fmt.Fprintf(&b, "• %s: файлов %d, папок %d, фото %d", folders[slug], c.Files, c.Items, c.Photos)
		if c.Media > 0 {
			fmt.Fprintf(&b, ", фото базы %d", c.Media)
		}
		b.WriteString("\n")
	}
	if t.Programs > 0 {
		wrote = true
		fmt.Fprintf(&b, "• %s: файлов %d\n", files.ProgramsFolder, t.Programs)
	}
	if t.Code > 0 {
		wrote = true
		fmt.Fprintf(&b, "• %s: файлов %d (архивы кода и Excel репозиториев)\n", files.CodeFolder, t.Code)
	}
	if !wrote {
		b.WriteString("• Сегодня ничего нового: всё, что есть в проектах, уже лежит здесь.\n")
	}

	var bad []string
	if cfg.Files != nil {
		slugs := make([]string, 0, len(cfg.Files.Projects))
		for k := range cfg.Files.Projects {
			slugs = append(slugs, k)
		}
		sort.Strings(slugs)
		for _, k := range slugs {
			for _, e := range cfg.Files.Projects[k].Errors {
				name := folders[k]
				if name == "" {
					name = k
				}
				bad = append(bad, fmt.Sprintf("• %s: %s", name, e))
			}
			if n := cfg.Files.Projects[k].Locked; n > 0 {
				bad = append(bad, fmt.Sprintf("• %s: открыты в Excel и не обновились — %d (обновятся в следующий раз)", folders[k], n))
			}
		}
	}
	b.WriteString("\nОШИБКИ ПОСЛЕДНЕЙ РАСКЛАДКИ\n")
	if len(bad) == 0 {
		b.WriteString("• Нет\n")
	} else {
		b.WriteString(strings.Join(bad, "\n") + "\n")
	}
	b.WriteString("\nКак открыть и вернуть файлы — «Как восстановить.txt» рядом.\n")
	text := b.String()
	if runtime.GOOS == "windows" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	_ = writeIfChanged(filepath.Join(root, TodayFile), append([]byte("\xef\xbb\xbf"), text...))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d КБ", (n+1023)/1024)
	}
}
