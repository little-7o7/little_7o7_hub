// Package update — программа обновляет себя сама (план pc-backup-1, решение 5). Хаб говорит последнюю версию и
// SHA-256 файла (из SHA256SUMS выпуска GitHub, little_7o7/src/lib/pc-release.ts); программа качает файл через хаб,
// сверяет размер и SHA-256, запускает новый файл с --self-test и только потом меняет себя. Старый файл остаётся
// рядом (.old), пока новая версия не запустилась, — не запустилась, возвращаем старую.
//
// Подписи ed25519 пока нет (ключ подписи пришлось бы хранить в секретах GitHub Actions — отложено): файл приходит
// из закрытого репозитория через хаб по https, и SHA-256 сверяется с выпуском.
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Target — имя файла выпуска для этой системы: windows-amd64, darwin-arm64, darwin-amd64.
func Target() string { return runtime.GOOS + "-" + runtime.GOARCH }

// SelfTestLine — что печатает --self-test исправная программа.
const SelfTestLine = "littlehub-ok"

func parts(v string) ([3]int, bool) {
	var out [3]int
	p := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(p) != 3 {
		return out, false
	}
	for i, s := range p {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Newer — версия b новее a (x.y.z). Непонятная версия — не новее.
func Newer(a, b string) bool {
	x, ok1 := parts(a)
	y, ok2 := parts(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return y[i] > x[i]
		}
	}
	return false
}

// SelfTest — запустить файл с --self-test: должен ответить «littlehub-ok <версия>» за 30 секунд.
func SelfTest(path, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--self-test").Output()
	if err != nil {
		return fmt.Errorf("новая версия не запустилась: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != SelfTestLine+" "+want {
		return fmt.Errorf("новая версия ответила не так: %q", got)
	}
	return nil
}

// Apply — поставить новый файл программы на место exe. test — проверка нового файла (обычно SelfTest).
func Apply(exe string, data []byte, test func(path string) error) error {
	next := exe + ".new"
	old := exe + ".old"
	_ = os.Remove(next)
	if err := os.WriteFile(next, data, 0o755); err != nil {
		return err
	}
	if err := test(next); err != nil {
		_ = os.Remove(next)
		return err
	}
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("не удалось отложить старую версию: %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe)
		_ = os.Remove(next)
		return fmt.Errorf("не удалось поставить новую версию: %w", err)
	}
	return nil
}

// Rollback — новая версия не поднялась: вернуть старую (.old). Нет старой — ошибка.
func Rollback(exe string) error {
	old := exe + ".old"
	if _, err := os.Stat(old); err != nil {
		return errors.New("старой версии нет")
	}
	_ = os.Remove(exe + ".bad")
	if err := os.Rename(exe, exe+".bad"); err != nil {
		return err
	}
	return os.Rename(old, exe)
}

// Cleanup — новая версия работает: старая больше не нужна.
func Cleanup(exe string) {
	_ = os.Remove(exe + ".old")
	_ = os.Remove(exe + ".bad")
	_ = os.Remove(exe + ".new")
}
