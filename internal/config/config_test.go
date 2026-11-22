package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUpdate(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(c *Config) {
		c.Hub = "https://hub.test"
		c.Disks = []string{"D:\\"}
		c.Copies["hub"] = Copy{Day: "2026-11-08", Bytes: 5}
	}); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	got.Disks[0] = "изменено снаружи"
	got.Copies["hub"] = Copy{}
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := s2.Get()
	if c.Hub != "https://hub.test" || c.Disks[0] != "D:\\" || c.Copies["hub"].Bytes != 5 {
		t.Fatalf("%+v", c)
	}
	if s.Get().Disks[0] != "D:\\" {
		t.Fatal("Get отдал не копию")
	}
	t.Setenv("LITTLEHUB_DIR", dir)
	if d, _ := Dir(); d != dir {
		t.Fatal(d)
	}
}

// Переезд хаба с Vercel (2026-11-22): старый адрес в config.json — новый, и он записан в файл; другие адреса не трогаем.
func TestLoadMovesOldHub(t *testing.T) {
	for _, old := range []string{OldHub, OldHub + "/"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"hub":"`+old+`","deviceId":3,"disks":["D:\\"]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Get(); got.Hub != NewHub || got.DeviceID != 3 || len(got.Disks) != 1 {
			t.Fatalf("после переезда: %+v", got)
		}
		s2, err := Load(dir)
		if err != nil || s2.Get().Hub != NewHub {
			t.Fatalf("в файле остался старый адрес: %v %q", err, s2.Get().Hub)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"hub":"https://hub.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil || s.Get().Hub != "https://hub.test" {
		t.Fatalf("чужой адрес поменялся: %v %q", err, s.Get().Hub)
	}
}
