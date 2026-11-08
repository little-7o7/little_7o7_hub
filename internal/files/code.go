package files

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/fileseal"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// CodeFolder — «Little Hub/Код/<репозиторий>/» (план pc-backup-2, этап 4, пункты 89–90): архив кода ветки по
// умолчанию «<репозиторий>-<день>.zip» — после нового push (один на день: в течение дня заменяется), старше 30 дней
// убираются, но последний остаётся всегда; «<репозиторий>.xlsx» — коммиты, задачи, запросы на слияние, выпуски,
// ветки — раз в день и после push. Всё под паролем копий. Репозиторий пропал с GitHub — его папку не трогаем.
const CodeFolder = "Код"

// CodeKeep — сколько дней хранить архивы кода (последний — всегда).
const CodeKeep = 30

// codeState — служебный «Код/.code.json»: какой push уже лежит архивом и в какой день записан Excel.
const codeState = ".code.json"

// CodeSource — откуда берётся код (хаб; в проверках — подставной).
type CodeSource interface {
	Code(ctx context.Context) ([]hub.CodeRepo, error)
	CodeFile(ctx context.Context, repo, what string, w io.Writer, max int64) (hub.Saved, error)
}

// CodeResult — репозиториев в списке, записано архивов и Excel, что не вышло.
type CodeResult struct {
	Repos  int
	Zips   int
	Excels int
	Errors []string
}

type codeKept struct {
	Pushed string `json:"pushed,omitempty"`
	Excel  string `json:"excel,omitempty"`
	// Empty — у репозитория нет кода (ни одного push): архива нет и не будет до первого push.
	Empty bool `json:"empty,omitempty"`
}

const maxCodeExcel = 64 << 20

// Code — разложить «Код»: на каждый репозиторий архив (если push новее того, что лежит, или архива нет) и Excel
// (если сегодня ещё не писали или был push). Сбой одного репозитория — в Errors, остальные идут дальше.
func Code(ctx context.Context, src CodeSource, d *Disk, now time.Time) (CodeResult, error) {
	res := CodeResult{Errors: []string{}}
	repos, err := src.Code(ctx)
	if errors.Is(err, hub.ErrNothing) || (err == nil && len(repos) == 0) {
		return res, nil // хаб старше или ключа GitHub в хабе нет
	}
	if err != nil {
		return res, err
	}
	dir := filepath.Join(d.Root, CodeFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	statePath := filepath.Join(dir, codeState)
	state := map[string]*codeKept{}
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &state)
	}
	seen := map[string]bool{}
	for _, r := range repos {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		key := strings.ToLower(r.Name)
		if !Ok(r.Name) || strings.HasPrefix(r.Name, ".") || seen[key] {
			continue
		}
		seen[key] = true
		res.Repos++
		k := state[r.Name]
		if k == nil {
			k = &codeKept{}
			state[r.Name] = k
		}
		rdir := filepath.Join(dir, r.Name)
		if err := os.MkdirAll(rdir, 0o755); err != nil {
			res.Errors = append(res.Errors, r.Name+": "+err.Error())
			continue
		}
		pushed := k.Pushed != r.PushedAt || (!k.Empty && len(codeZips(rdir, r.Name)) == 0)
		if pushed {
			zip := filepath.Join(rdir, r.Name+"-"+d.Today+".zip")
			_, ok, err := d.saveWith(zip, func(w io.Writer) (hub.Saved, error) { return src.CodeFile(ctx, r.Name, "zip", w, hub.MaxCode) })
			switch {
			case errors.Is(err, hub.ErrNothing):
				k.Pushed, k.Empty = r.PushedAt, true
			case err != nil:
				if Fatal(err) {
					return res, err
				}
				res.Errors = append(res.Errors, r.Name+": архив кода — "+err.Error())
			case ok:
				k.Pushed, k.Empty = r.PushedAt, false
				res.Zips++
			default:
				res.Errors = append(res.Errors, r.Name+": архив кода открыт и не заменился")
			}
		}
		if pushed || k.Excel != d.Today || (find(filepath.Join(rdir, r.Name+".xlsx")) == "" && !k.Empty) {
			_, ok, err := d.saveWith(filepath.Join(rdir, r.Name+".xlsx"), func(w io.Writer) (hub.Saved, error) {
				return src.CodeFile(ctx, r.Name, "excel", w, maxCodeExcel)
			})
			switch {
			case errors.Is(err, hub.ErrNothing):
				k.Excel = d.Today
			case err != nil:
				if Fatal(err) {
					return res, err
				}
				res.Errors = append(res.Errors, r.Name+": Excel — "+err.Error())
			case ok:
				k.Excel = d.Today
				res.Excels++
			default:
				res.Errors = append(res.Errors, r.Name+".xlsx открыт в Excel и не обновился")
			}
		}
		pruneZips(rdir, r.Name, now)
	}
	if b, err := json.Marshal(state); err == nil {
		_ = writeHidden(statePath, b)
	}
	return res, nil
}

// codeZips — архивы репозитория в папке (любом виде) по дням, старые первыми: день → путь.
func codeZips(dir, name string) [][2]string {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `-(\d{4}-\d{2}-\d{2})\.zip(` + regexp.QuoteMeta(fileseal.Ext) + `)?$`)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out [][2]string
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil && e.Type().IsRegular() && days.ValidDay(m[1]) {
			out = append(out, [2]string{m[1], filepath.Join(dir, e.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// pruneZips — архивы старше CodeKeep дней, кроме последнего.
func pruneZips(dir, name string, now time.Time) {
	zips := codeZips(dir, name)
	since := days.Since(now, CodeKeep)
	for i, z := range zips {
		if i < len(zips)-1 && z[0] < since {
			_ = os.Remove(z[1])
		}
	}
}
