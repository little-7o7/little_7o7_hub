package disks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPicksFirstWithRoom(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "нет")
	space := func(p string) (Space, error) {
		switch p {
		case a: // 4 ГБ свободно — мало
			return Space{Free: 4e9, Total: 1e12}, nil
		case b:
			return Space{Free: 200e9, Total: 1e12}, nil
		}
		return Space{}, errors.New("нет диска")
	}
	st, active, err := Check([]string{c, a, b}, space)
	if err != nil || active != b {
		t.Fatalf("active=%q err=%v", active, err)
	}
	if st[0].OK || st[0].Error != "диск недоступен" || st[1].OK || st[1].Error != "мало места" || !st[2].Active || !st[2].OK {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(Root(b)); err != nil {
		t.Fatal("папка программы не создана")
	}
}

func TestCheckShareAndNone(t *testing.T) {
	a := t.TempDir()
	// 6 ГБ из 100 ГБ — меньше 10%.
	_, _, err := Check([]string{a}, func(string) (Space, error) { return Space{Free: 6e9, Total: 100e9}, nil })
	if !errors.Is(err, ErrNoDisk) {
		t.Fatalf("%v", err)
	}
	_, active, err := Check([]string{a}, func(string) (Space, error) { return Space{Free: 11e9, Total: 100e9}, nil })
	if err != nil || active != a {
		t.Fatal(err)
	}
	if _, _, err := Check(nil, Free); !errors.Is(err, ErrNoDisk) {
		t.Fatal("пустой список")
	}
}

func TestRealFree(t *testing.T) {
	s, err := Free(t.TempDir())
	if err != nil || s.Total == 0 {
		t.Fatalf("%+v %v", s, err)
	}
	if Default() == "" {
		t.Fatal("нет диска по умолчанию")
	}
	_ = Suggest()
}

func TestClean(t *testing.T) {
	if Clean("/Volumes/HDD/") != "/Volumes/HDD" {
		t.Fatal(Clean("/Volumes/HDD/"))
	}
}
