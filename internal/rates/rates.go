// Package rates — курсы валют с узбекских сайтов (владелец 2026-11-08: «kurs tsentralnogo banka, i mnogo api kursov.
// tolko uzbekistanskix»; «adminka beryot kurs iz kompyuterov»). Серверы хаба (Vercel) не открывают cbu.uz и nbu.uz,
// а компьютер в Узбекистане открывает. Программа скачивает ответы источников и пересылает хабу как есть (gzip и
// base64), разбирает и сверяет их хаб (little_7o7/src/lib/fx.ts). Скачивает только адреса из своего списка, даже
// если хаб попросит другой.
package rates

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Allowed — источники, которые программа вообще согласна скачивать.
var Allowed = map[string]string{
	"cbu":    "https://cbu.uz/uz/arkhiv-kursov-valyut/json/",
	"nbu":    "https://nbu.uz/uz/exchange-rates/json/",
	"bankuz": "https://bank.uz/currency",
}

const (
	// Every — как часто отправлять курсы.
	Every   = 3 * time.Hour
	maxBody = 3 << 20
)

// Pick — источники хаба, которые есть и в своём списке (с тем же адресом). Хаб не ответил — весь свой список.
func Pick(fromHub []hub.Source) []hub.Source {
	if len(fromHub) == 0 {
		out := make([]hub.Source, 0, len(Allowed))
		for _, id := range []string{"cbu", "nbu", "bankuz"} {
			out = append(out, hub.Source{ID: id, URL: Allowed[id]})
		}
		return out
	}
	var out []hub.Source
	for _, s := range fromHub {
		if Allowed[s.ID] == s.URL {
			out = append(out, s)
		}
	}
	return out
}

// Fetch — скачать источник и упаковать для хаба. Не ответил — ошибка (хабу такое не отправляем).
func Fetch(ctx context.Context, c *http.Client, s hub.Source) (hub.RateItem, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	if err != nil {
		return hub.RateItem{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 LittleHub")
	req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.5")
	req.Header.Set("Accept-Language", "ru,uz;q=0.8,en;q=0.5")
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return hub.RateItem{}, fmt.Errorf("%s не открылся: %v", req.URL.Host, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return hub.RateItem{}, fmt.Errorf("%s оборвался: %v", req.URL.Host, err)
	}
	if len(body) > maxBody {
		return hub.RateItem{}, fmt.Errorf("%s: слишком большой ответ", req.URL.Host)
	}
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	_, _ = w.Write(body)
	_ = w.Close()
	return hub.RateItem{Source: s.ID, URL: s.URL, Status: resp.StatusCode, Body: base64.StdEncoding.EncodeToString(z.Bytes()), Gzip: true}, nil
}

// Collect — скачать все источники (каждый не дольше 30 секунд). Ответ — что скачалось и ошибки остальных.
func Collect(ctx context.Context, c *http.Client, sources []hub.Source) ([]hub.RateItem, []error) {
	var items []hub.RateItem
	var errs []error
	for _, s := range sources {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		it, err := Fetch(cctx, c, s)
		cancel()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		items = append(items, it)
	}
	return items, errs
}
