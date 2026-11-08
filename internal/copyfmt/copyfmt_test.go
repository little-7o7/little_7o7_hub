package copyfmt

import (
	"errors"
	"os"
	"testing"
)

// Образцы в testdata сделаны кодом хаба (sealWithPassword и seal из little_7o7/src/lib/copy-seal.ts): так программа
// открывает ровно то, что пишет хаб.
const pass = "образец-пароля-копий"

func TestOpenHubAndSarpo(t *testing.T) {
	want, _ := os.ReadFile("testdata/sample.sql")
	for _, f := range []string{"testdata/hub.l7copy", "testdata/sarpo.copy"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(b); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		got, err := Open(b, pass)
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s: %q %v", f, got, err)
		}
		if _, err := Open(b, "чужой пароль"); !errors.Is(err, ErrPassword) {
			t.Fatalf("%s: чужой пароль: %v", f, err)
		}
		bad := append([]byte(nil), b...)
		bad[len(bad)-40] ^= 1
		if _, err := Open(bad, pass); !errors.Is(err, ErrPassword) {
			t.Fatalf("%s: порча: %v", f, err)
		}
	}
}

func TestCheckRejects(t *testing.T) {
	b, _ := os.ReadFile("testdata/hub.l7copy")
	for name, x := range map[string][]byte{
		"чужая метка": append([]byte("XX-DB-1\n"), b[8:]...),
		"обрезан":     b[:len(b)-5],
		"короткий":    b[:40],
	} {
		if err := Check(x); !errors.Is(err, ErrNotCopy) {
			t.Errorf("%s: %v", name, err)
		}
	}
	many := append([]byte(nil), b...)
	many[8+16], many[8+17], many[8+18], many[8+19] = 0, 0, 0, 1
	if err := Check(many); !errors.Is(err, ErrNotCopy) {
		t.Error("1 повтор PBKDF2 — не наша копия")
	}
}
