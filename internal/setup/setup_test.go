package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "LittleHub-Setup.exe")
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTailRoundTrip(t *testing.T) {
	prog := []byte("MZ\x90\x00 программа")
	want := Info{Hub: "https://little-7o7.vercel.app", Code: "0A4C-8053-5DD6-700D"}
	p := write(t, append(append([]byte{}, prog...), Tail(want)...))
	got, size, ok, err := Read(p)
	if err != nil || !ok || got != want || size != int64(len(prog)) {
		t.Fatalf("got %+v size %d ok %v err %v", got, size, ok, err)
	}
}

// Хаб пишет хвост так же (tests/pc-setup.test.ts в little_7o7 сверяет эти байты).
func TestTailBytes(t *testing.T) {
	got := string(Tail(Info{Hub: "https://h", Code: "AB"}))
	want := `{"hub":"https://h","code":"AB"}` + "\x00\x00\x00\x00\x00\x00\x00\x1f" + Magic
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestNoTail(t *testing.T) {
	for name, b := range map[string][]byte{
		"обычная программа":  []byte("MZ just a program, no tail at all"),
		"короткий":           []byte("MZ"),
		"длина больше файла": append([]byte("{}"), append([]byte{0, 0, 0, 0, 0, 0, 0x10, 0}, Magic...)...),
		"не https":           append([]byte("MZ"), Tail(Info{Hub: "http://evil", Code: "X"})...),
		"без кода":           append([]byte("MZ"), Tail(Info{Hub: "https://h"})...),
		"не JSON":            append([]byte("MZ{oops"), append([]byte{0, 0, 0, 0, 0, 0, 0, 5}, Magic...)...),
	} {
		p := write(t, b)
		_, size, ok, err := Read(p)
		if ok || err != nil || size != int64(len(b)) {
			t.Fatalf("%s: ok %v size %d err %v", name, ok, size, err)
		}
	}
	if _, _, ok, err := Read(filepath.Join(t.TempDir(), "нет")); ok || err == nil {
		t.Fatal("нет файла — ошибка")
	}
}
