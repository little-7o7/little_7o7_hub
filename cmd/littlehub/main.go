// Little Hub — программа владельца little_7o7 для его компьютеров (офис, дом, Mac): хранит копии хаба по дням на
// своих дисках и присылает хабу курсы валют с узбекских сайтов. План: little_7o7/.claude/plans/pc-backup-1.plan.md.
//
//	LittleHub                               — работать (значок, окно, копии, курсы, обновления)
//	LittleHub --install --hub URL --pair КОД — поставить себя, подключиться к хабу, автозапуск (команда установки)
//	LittleHub --uninstall                   — убрать автозапуск и ключ компьютера (копии на дисках остаются)
//	LittleHub --self-test                   — проверка новой версии перед обновлением
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/app"
	"github.com/little-7o7/little_7o7_hub/internal/autostart"
	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
	"github.com/little-7o7/little_7o7_hub/internal/secret"
	"github.com/little-7o7/little_7o7_hub/internal/tray"
	"github.com/little-7o7/little_7o7_hub/internal/ui"
	"github.com/little-7o7/little_7o7_hub/internal/update"
)

// version — ставит сборка: -ldflags "-X main.version=1.2.3".
var version = "0.0.0-dev"

// Коды выхода команды установки (install.ps1 и install.sh их показывают).
const (
	exitBadCode = 3
	exitNoHub   = 4
	exitFailed  = 5
)

func main() {
	selfTest := flag.Bool("self-test", false, "")
	install := flag.Bool("install", false, "")
	uninstall := flag.Bool("uninstall", false, "")
	hubURL := flag.String("hub", "", "")
	pair := flag.String("pair", "", "")
	updated := flag.Bool("updated", false, "")
	flag.Parse()
	switch {
	case *selfTest:
		os.Exit(runSelfTest())
	case *install:
		os.Exit(runInstall(*hubURL, *pair))
	case *uninstall:
		os.Exit(runUninstall())
	default:
		os.Exit(run(*updated))
	}
}

func runSelfTest() int {
	if _, err := config.Dir(); err != nil {
		fmt.Println("нет папки настроек:", err)
		return 1
	}
	if _, err := days.Rel(app.HubProject, days.Today(time.Now())); err != nil {
		fmt.Println(err)
		return 1
	}
	fmt.Println(update.SelfTestLine, version)
	return 0
}

func newApp(dir string) (*app.App, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	return &app.App{Version: version, Dir: dir, Cfg: cfg, Secret: secret.New(dir), Space: disks.Free}, nil
}

// waitAlive — дождаться, пока запущенная программа ответит из окна (после установки — открыть его владельцу).
func waitAlive(dir string, d time.Duration) (ui.Info, bool) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if i, err := ui.ReadInfo(dir); err == nil && i.Alive() {
			return i, true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return ui.Info{}, false
}

// stopRunning — попросить уже запущенную программу выйти и дождаться.
func stopRunning(dir string) {
	i, err := ui.ReadInfo(dir)
	if err != nil || !i.Alive() {
		return
	}
	i.Quit()
	for n := 0; n < 30 && i.Alive(); n++ {
		time.Sleep(300 * time.Millisecond)
	}
}

func runInstall(hubURL, code string) int {
	dir, err := config.Dir()
	if err != nil {
		fmt.Println("Little Hub:", err)
		return exitFailed
	}
	base, err := hub.CheckBase(hubURL)
	if err != nil {
		fmt.Println("Little Hub:", err)
		return exitFailed
	}
	stopRunning(dir)
	dst, err := autostart.InstallPath()
	if err == nil {
		err = autostart.CopySelf(dst)
	}
	if err != nil {
		fmt.Println("Little Hub: не удалось поставить программу:", err)
		return exitFailed
	}
	a, err := newApp(dir)
	if err != nil {
		fmt.Println("Little Hub:", err)
		return exitFailed
	}
	if code != "" || !a.Paired() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		p, err := a.Pair(ctx, base, code)
		cancel()
		switch {
		case errors.Is(err, hub.ErrBadCode):
			fmt.Println("Little Hub:", err)
			return exitBadCode
		case err != nil:
			fmt.Println("Little Hub:", err)
			return exitNoHub
		}
		fmt.Printf("Little Hub: компьютер подключён как «%s»\n", p.Name)
	}
	if err := autostart.Enable(dst); err != nil {
		fmt.Println("Little Hub: автозапуск не включился:", err)
	}
	if _, ok := waitAlive(dir, 2*time.Second); !ok {
		_ = autostart.Start(dst)
	}
	if i, ok := waitAlive(dir, 20*time.Second); ok {
		_ = ui.OpenBrowser(i.URL())
	}
	fmt.Println("Little Hub: установлено:", dst)
	return 0
}

func runUninstall() int {
	dir, err := config.Dir()
	if err != nil {
		return exitFailed
	}
	stopRunning(dir)
	_ = autostart.Disable()
	_ = secret.New(dir).Delete()
	fmt.Println("Little Hub: автозапуск и ключ компьютера убраны. Копии на дисках остались.")
	return 0
}

func run(updated bool) int {
	dir, err := config.Dir()
	if err != nil {
		return exitFailed
	}
	_ = os.MkdirAll(dir, 0o700)
	app.TrimLog(dir)
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	installed, _ := autostart.InstallPath()
	// Вторая копия программы — открыть окно первой и выйти. После обновления старая ещё может выходить — ждём её.
	if i, err := ui.ReadInfo(dir); err == nil && i.Alive() {
		if !updated {
			_ = ui.OpenBrowser(i.URL())
			return 0
		}
		for n := 0; n < 50 && i.Alive(); n++ {
			time.Sleep(300 * time.Millisecond)
		}
	}
	a, err := newApp(dir)
	if err != nil {
		return exitFailed
	}
	if exe == installed {
		a.Exe = exe
	}
	ctx, cancel := context.WithCancel(context.Background())
	var server *ui.Server
	quit := func() {
		cancel()
		if server != nil {
			server.Close()
		}
		tray.Stop()
	}
	a.OnRestart = func() {
		if err := autostart.Restart(exe); err != nil {
			a.Logf(true, "Перезапуск не удался: %v", err)
			return
		}
		quit()
	}
	server, ln, err := ui.Listen(a, dir, quit)
	if err != nil {
		a.Logf(true, "Окно не открылось: %v", err)
		if updated && a.Exe != "" && update.Rollback(exe) == nil {
			_ = autostart.Restart(exe)
		}
		return exitFailed
	}
	go func() { _ = server.Serve(ln) }()
	if updated {
		update.Cleanup(exe)
	}
	a.Logf(false, "Little Hub %s запущен", version)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.Logf(true, "Сбой: %v — перезапуск", r)
				_ = autostart.Restart(exe)
				quit()
			}
		}()
		a.Run(ctx)
	}()
	if !a.Paired() {
		_ = ui.OpenBrowser(server.Info().URL())
	}
	tray.Run(tray.Menu{
		Open: func() { _ = ui.OpenBrowser(server.Info().URL()) },
		Save: a.Kick,
		Quit: quit,
		Lang: func() string { return a.Cfg.Get().Lang },
		Tooltip: func() string {
			c := a.Cfg.Get().Copies[app.HubProject.Slug]
			if c.At == "" {
				return "Little Hub"
			}
			t, _ := time.Parse(time.RFC3339, c.At)
			return "Little Hub — " + t.In(days.Tashkent).Format("02.01 15:04")
		},
	}, nil)
	cancel()
	return 0
}
