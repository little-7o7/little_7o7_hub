package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// «Программы» (план pc-backup-2, этап 4): установщики всех версий — один раз, с проверкой размера и SHA-256; тексты
// выпусков — если поправили; чужие имена и пути — мимо.

type fakePrograms struct {
	list  []hub.Program
	files map[string][]byte
	got   []string
	err   error
}

func (f *fakePrograms) Programs(context.Context) ([]hub.Program, error) { return f.list, f.err }
func (f *fakePrograms) ProgramFile(_ context.Context, p hub.Program, w io.Writer) (int64, error) {
	f.got = append(f.got, p.Name)
	b, ok := f.files[p.URL]
	if !ok {
		return 0, &hub.APIError{Status: 404, Msg: "нет"}
	}
	n, err := w.Write(b)
	return int64(n), err
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestPrograms(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	exe, plugin, mac := []byte("MZ-littlehub"), []byte("MZ-strassio"), []byte("\xcf\xfa\xed\xfe")
	f := &fakePrograms{
		files: map[string][]byte{"/api/pc/asset/21": exe, hub.PluginReleases + "v0.9.1/S.exe": plugin, "/api/pc/asset/22": mac, "/api/pc/asset/23": []byte("испорчен")},
		list: []hub.Program{
			{Dir: "Little Hub", Name: "LittleHub-1.2.1-windows.exe", URL: "/api/pc/asset/21", Size: int64(len(exe)), Sha256: sum(exe)},
			{Dir: "Little Hub", Name: "LittleHub-1.2.1-mac-arm64", URL: "/api/pc/asset/22", Size: int64(len(mac))},
			{Dir: "Little Hub", Name: "Что нового 1.2.1.txt", Text: "Что нового\n- быстрее"},
			{Dir: "Strassio (плагин)/0.9.1", Name: "S.exe", URL: hub.PluginReleases + "v0.9.1/S.exe", Size: int64(len(plugin)), Sha256: strings.ToUpper(sum(plugin))},
			{Dir: "Little Hub", Name: "broken.exe", URL: "/api/pc/asset/23", Size: 16, Sha256: sum([]byte("другой"))},
			{Dir: "Little Hub", Name: "missing.exe", URL: "/api/pc/asset/99", Size: 5},
			{Dir: "../x", Name: "evil.exe", URL: "/api/pc/asset/21"},
			{Dir: "a/b/c/d", Name: "deep.exe", URL: "/api/pc/asset/21"},
			{Dir: "Little Hub", Name: "bad:name.exe", URL: "/api/pc/asset/21"},
			{Dir: "", Name: "root.exe", URL: "/api/pc/asset/21"},
			{Dir: "little hub", Name: "LITTLEHUB-1.2.1-WINDOWS.EXE", URL: "/api/pc/asset/21"}, // тёзка без учёта регистра
		},
	}
	res, err := Programs(ctx, f, root)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 4 || res.Have != 0 || len(res.Errors) != 2 {
		t.Fatalf("%+v", res)
	}
	dir := filepath.Join(root, ProgramsFolder)
	for p, want := range map[string][]byte{
		"Little Hub/LittleHub-1.2.1-windows.exe": exe,
		"Little Hub/LittleHub-1.2.1-mac-arm64":   mac,
		"Strassio (плагин)/0.9.1/S.exe":          plugin,
		"Little Hub/Что нового 1.2.1.txt":        nil,
	} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(p, err)
		}
		if want != nil && string(b) != string(want) {
			t.Fatal(p, string(b))
		}
		if want == nil && !strings.HasPrefix(string(b), "\xef\xbb\xbfЧто нового") {
			t.Fatal(p, string(b))
		}
	}
	for _, p := range []string{"Little Hub/broken.exe", "Little Hub/broken.exe" + tmp, "Little Hub/missing.exe", "x/evil.exe", "a"} {
		if exists(filepath.Join(dir, filepath.FromSlash(p))) {
			t.Fatal("лишний файл", p)
		}
	}
	if exists(filepath.Join(root, "x")) || exists(filepath.Join(root, "root.exe")) {
		t.Fatal("вне «Программ»")
	}

	// Второй раз: что есть — не качаем; поправили текст выпуска — заменяем.
	f.got = nil
	f.list[2].Text = "Что нового\n- быстрее\n- ещё"
	res, err = Programs(ctx, f, root)
	if err != nil || res.Files != 1 || res.Have != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Join(f.got, ",") != "broken.exe,missing.exe" {
		t.Fatal("скачано заново:", f.got)
	}

	// Хаб старше («Программ» нет) — ничего; хаб не ответил — ошибка.
	if res, err := Programs(ctx, &fakePrograms{err: hub.ErrNothing}, root); err != nil || res.Files != 0 {
		t.Fatal(res, err)
	}
	if _, err := Programs(ctx, &fakePrograms{err: errors.New("нет связи")}, root); err == nil {
		t.Fatal("ошибка потерялась")
	}
}
