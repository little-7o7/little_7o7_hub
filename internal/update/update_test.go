package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
