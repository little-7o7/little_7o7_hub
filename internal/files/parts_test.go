package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cut — копия name частями по size байт в dir (как их шлёт бот хаба).
func cut(t *testing.T, dir, name string, data []byte, size int) []string {
	t.Helper()
	n := (len(data) + size - 1) / size
	var out []string
	for k := 1; k <= n; k++ {
		p := filepath.Join(dir, name+".part"+strconv.Itoa(k)+"-of-"+strconv.Itoa(n))
		if err := os.WriteFile(p, data[(k-1)*size:min(k*size, len(data))], 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestJoinParts(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("копия хаба "), 1000)
	parts := cut(t, dir, "hub-2026-11-23.l7copy", data, 8000) // 20000 байт: 8000 + 8000 + 4000
	if len(parts) != 3 {
		t.Fatal(len(parts))
	}
	// Чужая копия частями в той же папке не мешает.
	cut(t, dir, "sarpo-2026-11-23.l7copy", []byte("другая копия"), 5)
	j, err := JoinParts(`"` + parts[1] + `"`)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if j.Path != filepath.Join(dir, "hub-2026-11-23.l7copy") || j.Bytes != int64(len(data)) || j.Parts != 3 || j.Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatal(j)
	}
	if b, _ := os.ReadFile(j.Path); !bytes.Equal(b, data) {
		t.Fatal("склеено не так")
	}
	// Копия уже есть — не перезаписываем.
	_ = os.WriteFile(j.Path, []byte("своя"), 0o644)
	if _, err := JoinParts(parts[0]); err == nil || !strings.Contains(err.Error(), "уже есть") {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(j.Path); string(b) != "своя" {
		t.Fatal("перезаписан")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatal("остался временный файл", left)
	}
}

func TestJoinPartsErrors(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 100)
	cases := map[string]func(dir string) string{
		"нет части": func(dir string) string {
			p := cut(t, dir, "a.l7copy", data, 40)
			_ = os.Remove(p[1])
			return p[0]
		},
		"лишняя часть": func(dir string) string {
			p := cut(t, dir, "a.l7copy", data, 40)
			_ = os.WriteFile(filepath.Join(dir, "a.l7copy.part4-of-3"), []byte("x"), 0o644)
			return p[0]
		},
		"чужая N": func(dir string) string {
			p := cut(t, dir, "a.l7copy", data, 40)
			_ = os.WriteFile(filepath.Join(dir, "a.l7copy.part2-of-4"), []byte("x"), 0o644)
			return p[0]
		},
		"другой размер": func(dir string) string {
			p := cut(t, dir, "a.l7copy", data, 40)
			_ = os.WriteFile(p[1], []byte("короче"), 0o644)
			return p[0]
		},
		"последняя больше": func(dir string) string {
			p := cut(t, dir, "a.l7copy", data, 40)
			_ = os.WriteFile(p[2], bytes.Repeat([]byte("y"), 41), 0o644)
			return p[0]
		},
		"без .partK-of-N": func(dir string) string {
			p := filepath.Join(dir, "a.l7copy")
			_ = os.WriteFile(p, data, 0o644)
			return p
		},
		"одна часть": func(dir string) string { return cut(t, dir, "a.l7copy", data, 100)[0] },
		"больше 20":  func(dir string) string { return filepath.Join(dir, "a.l7copy.part1-of-21") },
		"номер больше N": func(dir string) string {
			return filepath.Join(dir, "a.l7copy.part3-of-2")
		},
		"нули впереди": func(dir string) string { return filepath.Join(dir, "a.l7copy.part01-of-03") },
		// «...part1-of-2» — имя копии «..»: копия легла бы в папку выше.
		"имя ..": func(dir string) string {
			_ = os.WriteFile(filepath.Join(dir, "...part1-of-2"), []byte("ab"), 0o644)
			_ = os.WriteFile(filepath.Join(dir, "...part2-of-2"), []byte("c"), 0o644)
			return filepath.Join(dir, "...part1-of-2")
		},
	}
	for name, setup := range cases {
		root := t.TempDir()
		dir := filepath.Join(root, "parts")
		_ = os.Mkdir(dir, 0o755)
		if j, err := JoinParts(setup(dir)); err == nil {
			t.Errorf("%s: собралось %v", name, j)
		}
		if list, _ := os.ReadDir(root); len(list) != 1 {
			t.Errorf("%s: что-то легло рядом с папкой частей", name)
		}
	}
	// Путь с «..» к частям — та же папка, что и без него: копия ложится рядом с частями.
	root := t.TempDir()
	dir := filepath.Join(root, "parts")
	_ = os.Mkdir(dir, 0o755)
	p := cut(t, dir, "a.l7copy", data, 40)
	j, err := JoinParts(filepath.Join(dir, "..", "parts", filepath.Base(p[0])))
	if err != nil || j.Path != filepath.Join(dir, "a.l7copy") {
		t.Fatal(j, err)
	}
}
