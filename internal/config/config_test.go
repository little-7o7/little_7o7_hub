package config

import "testing"

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
