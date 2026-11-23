// Package update — программа обновляет себя сама (план pc-backup-1, решение 5). Хаб говорит последнюю версию,
// SHA-256 файла (из SHA256SUMS выпуска GitHub, little_7o7/src/lib/pc-release.ts) и подпись; программа сверяет
// подпись, качает файл через хаб, сверяет размер и SHA-256, запускает новый файл с --self-test и только потом меняет
// себя. Старый файл остаётся рядом (.old), пока новая версия не запустилась, — не запустилась, возвращаем старую.
//
// Подпись — ECDSA P-256 (DER, base64) над SHA-256 текста «littlehub:<версия>:<sha256>»: закрытый ключ — только на
// Mac владельца, выпуск подписывает скрипт хаба после сборки. Без верной подписи обновление не ставится — даже если
// хаб или GitHub подменили и файл, и его SHA-256.
package update

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
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

// releaseKeyPEM — открытый ключ подписи выпусков (не секрет).
const releaseKeyPEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAECQ3AuwfAnb6u5XdRK9OceqDo9fkq
jpQM+Q3Ec2TyJMebzu0Ip3fiza0ZngI03kG7/gy1K49VaskW5E33uIfpEw==
-----END PUBLIC KEY-----`

// releaseKey — ключ, которым сверяем подпись (тесты подставляют свой). Не разобрался — nil: обновления не ставятся.
var releaseKey, _ = parseKey(releaseKeyPEM)

func parseKey(s string) (*ecdsa.PublicKey, error) {
	b, _ := pem.Decode([]byte(s))
	if b == nil || b.Type != "PUBLIC KEY" {
		return nil, errors.New("ключ подписи — не PEM PUBLIC KEY")
	}
	k, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PublicKey)
	if !ok || ek.Curve != elliptic.P256() {
		return nil, errors.New("ключ подписи — не ECDSA P-256")
	}
	return ek, nil
}

// Signed — подпись sig (base64 DER) выпуска version с файлом sha (hex) верна.
func Signed(version, sha, sig string) bool {
	der, err := base64.StdEncoding.DecodeString(sig)
	if releaseKey == nil || err != nil || len(der) == 0 {
		return false
	}
	h := sha256.Sum256([]byte("littlehub:" + strings.TrimPrefix(version, "v") + ":" + strings.ToLower(sha)))
	return ecdsa.VerifyASN1(releaseKey, h[:], der)
}

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
