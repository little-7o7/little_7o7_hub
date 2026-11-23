package update

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sign(t *testing.T, k *ecdsa.PrivateKey, msg string) string {
	t.Helper()
	h := sha256.Sum256([]byte(msg))
	der, err := ecdsa.SignASN1(rand.Reader, k, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func useKey(t *testing.T, k *ecdsa.PublicKey) {
	old := releaseKey
	releaseKey = k
	t.Cleanup(func() { releaseKey = old })
}

func TestSigned(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	useKey(t, &k.PublicKey)
	sha := strings.Repeat("ab", 32)
	good := sign(t, k, "littlehub:1.4.0:"+sha)
	// Хаб может прислать версию с «v» и SHA-256 заглавными — подписано всё равно «1.4.0» и строчными.
	if !Signed("1.4.0", sha, good) || !Signed("v1.4.0", strings.ToUpper(sha), good) {
		t.Fatal("верная подпись не прошла")
	}
	der, _ := base64.StdEncoding.DecodeString(good)
	der[len(der)-1] ^= 1
	bad := map[string][3]string{
		"подделанная":   {"1.4.0", sha, base64.StdEncoding.EncodeToString(der)},
		"чужой ключ":    {"1.4.0", sha, sign(t, other, "littlehub:1.4.0:"+sha)},
		"другая версия": {"1.4.1", sha, good},
		"другой sha256": {"1.4.0", strings.Repeat("cd", 32), good},
		"пустая":        {"1.4.0", sha, ""},
		"не base64":     {"1.4.0", sha, "не подпись"},
		"другой текст":  {"1.4.0", sha, sign(t, k, "1.4.0:"+sha)},
	}
	for name, c := range bad {
		if Signed(c[0], c[1], c[2]) {
			t.Error(name, "— прошла")
		}
	}
	// Ключа нет — ничего не проходит.
	useKey(t, nil)
	if Signed("1.4.0", sha, good) {
		t.Fatal("без ключа прошла")
	}
}

// Подпись, сделанная openssl (как подпишет скрипт хаба): openssl dgst -sha256 -sign key.pem | base64.
func TestSignedOpenSSL(t *testing.T) {
	k, err := parseKey(`-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEP8tStSaDIZZMnx2DEbcpVGkYrNis
e/scplb20tiEQd8hXZVmhDBJQfY930axUeuEqflsfcBjy+o8Z+idMlV9Mw==
-----END PUBLIC KEY-----`)
	if err != nil {
		t.Fatal(err)
	}
	useKey(t, k)
	sum := sha256.Sum256([]byte("PROGRAM"))
	sha := hex.EncodeToString(sum[:])
	sig := "MEYCIQCrOjhXAPSLunvy80v1VzWvwaBdor4d1FrFvsHmy0S6EAIhAKASz8m/gIF7ksQ5z/FS8yXBhnlJuDK5oU7h4+rCLkQ8"
	if !Signed("1.4.0", sha, sig) {
		t.Fatal("подпись openssl не прошла")
	}
	if Signed("1.4.1", sha, sig) {
		t.Fatal("чужая версия прошла")
	}
}

func TestReleaseKey(t *testing.T) {
	k, err := parseKey(releaseKeyPEM)
	if err != nil || k.Curve != elliptic.P256() || releaseKey == nil || !releaseKey.Equal(k) {
		t.Fatal("встроенный ключ не разобрался:", err)
	}
	// Не P-256 и не PEM — не ключ подписи.
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	if _, err := parseKey(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))); err == nil {
		t.Fatal("P-384 принят")
	}
	if _, err := parseKey("не ключ"); err == nil {
		t.Fatal("мусор принят")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.0.0", "1.0.1", true}, {"1.2.9", "1.10.0", true}, {"1.0.0", "1.0.0", false}, {"2.0.0", "1.9.9", false}, {"dev", "1.0.0", false}, {"1.0.0", "v1.1.0", true}, {"1.0", "1.1.0", false}}
	for _, c := range cases {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
	if Target() != runtime.GOOS+"-"+runtime.GOARCH {
		t.Fatal(Target())
	}
}

func TestApplyAndRollback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "LittleHub")
	_ = os.WriteFile(exe, []byte("старая"), 0o755)
	// Проверка не прошла — ничего не меняем.
	if err := Apply(exe, []byte("плохая"), func(string) error { return errors.New("не запустилась") }); err == nil {
		t.Fatal("ждали ошибку")
	}
	if b, _ := os.ReadFile(exe); string(b) != "старая" {
		t.Fatal(string(b))
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new остался")
	}
	var tested string
	if err := Apply(exe, []byte("новая"), func(p string) error { tested = p; return nil }); err != nil {
		t.Fatal(err)
	}
	if tested != exe+".new" {
		t.Fatal(tested)
	}
	if b, _ := os.ReadFile(exe); string(b) != "новая" {
		t.Fatal(string(b))
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "старая" {
		t.Fatal(string(b))
	}
	if err := Rollback(exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "старая" {
		t.Fatal("откат:", string(b))
	}
	Cleanup(exe)
	if err := Rollback(exe); err == nil {
		t.Fatal("старой уже нет")
	}
}

func TestSelfTest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok")
	_ = os.WriteFile(ok, []byte("#!/bin/sh\necho littlehub-ok 1.2.0\n"), 0o755)
	if err := SelfTest(ok, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := SelfTest(ok, "1.3.0"); err == nil {
		t.Fatal("не та версия")
	}
	bad := filepath.Join(dir, "bad")
	_ = os.WriteFile(bad, []byte("#!/bin/sh\nexit 3\n"), 0o755)
	if err := SelfTest(bad, "1.2.0"); err == nil {
		t.Fatal("упала")
	}
}
