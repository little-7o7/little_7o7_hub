package files

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Копия «за сегодня» и ночная копия пропущенного дня (план pc-backup-2).

func copyBody() []byte {
	body := append([]byte("L7-DB-1\n"), make([]byte, 16)...)
	body = append(body, 0, 0, 3, 0xe8)
	return append(body, make([]byte, 16+32+32)...)
}

func TestLiveAndNightCopy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := days.Project{Slug: "sarpo", Folder: "Sarpo Business", Ext: "l7copy"}
	body := copyBody()
	var sent url.Values
	f := &fake{backup: func(q url.Values, w io.Writer) (hub.Saved, error) {
		sent = q
		if q.Get("have") == "1794135900000" {
			return hub.Saved{}, hub.ErrNothing
		}
		n, _ := io.Copy(w, bytes.NewReader(body))
		name, day := "sarpo-2026-11-08.sql.gz", "2026-11-08"
		if q.Get("name") != "" {
			name, day = q.Get("name"), "2026-11-06"
		}
		h := map[string][]string{"X-Copy-Name": {name}, "X-Copy-Day": {day}, "X-Copy-Bytes": {strconv.Itoa(len(body))}}
		if q.Get("live") == "1" {
			h["X-Copy-Made"] = []string{"1794135960000"}
		}
		return hub.Saved{Bytes: n, Header: h}, nil
	}}
	c, err := LiveCopy(ctx, f, p, root, 1794135950000, 0)
	if err != nil || c.Made != 1794135960000 || c.Day != "2026-11-08" || sent.Get("live") != "1" || sent.Get("t") != "1794135950000" || sent.Has("have") {
		t.Fatalf("копия «за сегодня»: %+v %v %v", c, sent, err)
	}
	if read(t, filepath.Join(root, "Sarpo Business", "Копии базы", "2026-11", "sarpo-2026-11-08.l7copy")) != string(body) {
		t.Fatal("файл сегодняшнего дня")
	}
	if _, err := LiveCopy(ctx, f, p, root, 1794135950000, 1794135900000); err != hub.ErrNothing || sent.Get("have") != "1794135900000" {
		t.Fatal("та же копия", err, sent)
	}
	// Ночная копия от 7-го — это конец 6-го: кладём в файл 6-го, хотя проект сказал свой день.
	c, err = NightCopy(ctx, f, p, root, "sarpo-2026-11-07.sql.gz", "2026-11-05")
	if err != nil || c.Day != "2026-11-05" || sent.Get("name") != "sarpo-2026-11-07.sql.gz" {
		t.Fatalf("ночная: %+v %v", c, err)
	}
	if !days.Exists(filepath.Join(root, "Sarpo Business", "Копии базы", "2026-11", "sarpo-2026-11-05.l7copy")) {
		t.Fatal("ночная копия не в своём дне")
	}
	for _, bad := range [][2]string{{"../x.sql.gz", "2026-11-05"}, {"sarpo-2026-11-07.sql.gz", "05.11.2026"}} {
		if _, err := NightCopy(ctx, f, p, root, bad[0], bad[1]); err == nil {
			t.Fatal("принята неверная копия", bad)
		}
	}
	// Ответ без имени — не ставим.
	f.backup = func(q url.Values, w io.Writer) (hub.Saved, error) {
		n, _ := io.Copy(w, bytes.NewReader(body))
		return hub.Saved{Bytes: n, Header: map[string][]string{"X-Copy-Bytes": {strconv.Itoa(len(body))}}}, nil
	}
	if _, err := LiveCopy(ctx, f, p, root, 1, 0); !hub.IsAPI(err) {
		t.Fatal("без имени", err)
	}
}

func TestNightName(t *testing.T) {
	for in, want := range map[string]string{
		"sarpo-2026-11-08.sql.gz": "2026-11-07",
		"sarpo-2026-11-01.sql.gz": "2026-10-31",
		"sarpo-2026-01-01.sql.gz": "2025-12-31",
		"sarpo-2026-13-01.sql.gz": "",
		"sarpo.sql.gz":            "",
		"":                        "",
	} {
		if got := NightName(in); got != want {
			t.Fatal(in, got)
		}
	}
}
