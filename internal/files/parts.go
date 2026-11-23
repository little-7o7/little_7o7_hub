package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Копия больше 50 МБ приходит из Telegram (бот хаба) частями по 45 МБ: «<имя копии>.part<K>-of-<N>», K с 1 без нулей
// впереди, N от 2 до 20; части — куски файла подряд. JoinParts склеивает их обратно рядом с частями.

var partRe = regexp.MustCompile(`^(.+)\.part([1-9][0-9]?)-of-([1-9][0-9]?)$`)

const maxParts = 20

// Joined — собранная копия: SHA-256 сверить с подписью последней части в Telegram.
type Joined struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Sha256 string `json:"sha256"`
	Parts  int    `json:"parts"`
}

// JoinParts — path: любая часть копии. Все части 1…N — в той же папке, лишних нет, все, кроме последней, одного
// размера; копия ложится рядом под своим именем, если такого файла ещё нет.
func JoinParts(path string) (Joined, error) {
	path = filepath.Clean(strings.Trim(strings.TrimSpace(path), `"'`)) // «Копировать как путь» в Windows — в кавычках
	m := partRe.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return Joined{}, errors.New("это не часть копии: имя должно кончаться на .part<номер>-of-<сколько>, например hub-2026-11-23.l7copy.part1-of-3")
	}
	base := m[1]
	n, _ := strconv.Atoi(m[3])
	if k, _ := strconv.Atoi(m[2]); n < 2 || n > maxParts || k > n {
		return Joined{}, fmt.Errorf("неверный номер части: %s", filepath.Base(path))
	}
	if !Ok(base) { // «..», «.», служебные имена: копия не выходит из папки частей
		return Joined{}, fmt.Errorf("неверное имя копии: %q", base)
	}
	dir := filepath.Dir(path)
	list, err := os.ReadDir(dir)
	if err != nil {
		return Joined{}, fmt.Errorf("папка с частями не открылась: %w", err)
	}
	parts := make([]string, n+1)
	sizes := make([]int64, n+1)
	for _, e := range list {
		pm := partRe.FindStringSubmatch(e.Name())
		if pm == nil || pm[1] != base {
			continue
		}
		k, _ := strconv.Atoi(pm[2])
		if pm[3] != m[3] || k > n {
			return Joined{}, fmt.Errorf("в папке лишняя часть %s: у этой копии %d частей — уберите чужие части", e.Name(), n)
		}
		info, err := e.Info()
		if err != nil {
			return Joined{}, err
		}
		if !info.Mode().IsRegular() {
			return Joined{}, fmt.Errorf("%s — не файл", e.Name())
		}
		parts[k], sizes[k] = filepath.Join(dir, e.Name()), info.Size()
	}
	var missing []string
	for k := 1; k <= n; k++ {
		if parts[k] == "" {
			missing = append(missing, strconv.Itoa(k))
		}
	}
	if missing != nil {
		return Joined{}, fmt.Errorf("нет частей %s из %d — скачайте их из Telegram в ту же папку", strings.Join(missing, ", "), n)
	}
	for k := 2; k <= n; k++ {
		if (k < n && sizes[k] != sizes[1]) || (k == n && (sizes[k] == 0 || sizes[k] > sizes[1])) {
			return Joined{}, fmt.Errorf("часть %d другого размера — скачайте её заново", k)
		}
	}
	out := filepath.Join(dir, base)
	if _, err := os.Lstat(out); err == nil {
		return Joined{}, fmt.Errorf("файл %s уже есть — не перезаписываю", out)
	}
	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return Joined{}, err
	}
	h := sha256.New()
	var total int64
	err = func() error {
		for _, p := range parts[1:] {
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			c, err := io.Copy(io.MultiWriter(tmp, h), in)
			in.Close()
			if err != nil {
				return err
			}
			total += c
		}
		return tmp.Sync()
	}()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		if _, serr := os.Lstat(out); serr == nil {
			err = fmt.Errorf("файл %s уже есть — не перезаписываю", out)
		} else {
			err = os.Rename(tmp.Name(), out)
		}
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return Joined{}, err
	}
	return Joined{Path: out, Bytes: total, Sha256: hex.EncodeToString(h.Sum(nil)), Parts: n}, nil
}
