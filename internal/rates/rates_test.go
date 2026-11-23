package rates

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

func TestPick(t *testing.T) {
	if got := Pick(nil); len(got) != 3 || got[0].ID != "cbu" {
		t.Fatal(got)
	}
	got := Pick([]hub.Source{{ID: "cbu", URL: Allowed["cbu"]}, {ID: "cbu", URL: "https://evil.example/"}, {ID: "x", URL: "https://x"}})
	if len(got) != 1 || got[0].URL != Allowed["cbu"] {
		t.Fatal(got)
	}
}

func TestFetchAndCollect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "Mozilla") {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/big" {
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxBody+10))
			return
		}
		_, _ = w.Write([]byte(`[{"Ccy":"USD"}]`))
	}))
	defer srv.Close()
	it, err := Fetch(context.Background(), srv.Client(), hub.Source{ID: "cbu", URL: srv.URL + "/json"})
	if err != nil || it.Status != 200 || !it.Gzip {
		t.Fatal(it, err)
	}
	raw, _ := base64.StdEncoding.DecodeString(it.Body)
	zr, _ := gzip.NewReader(bytes.NewReader(raw))
	plain, _ := io.ReadAll(zr)
	if string(plain) != `[{"Ccy":"USD"}]` {
		t.Fatal(string(plain))
	}
	items, errs := Collect(context.Background(), srv.Client(), []hub.Source{{ID: "cbu", URL: srv.URL + "/json"}, {ID: "nbu", URL: srv.URL + "/big"}, {ID: "bankuz", URL: "http://127.0.0.1:1/"}})
	if len(items) != 1 || len(errs) != 2 {
		t.Fatal(len(items), errs)
	}
}

func TestPickGold(t *testing.T) {
	if got := PickGold(nil); len(got) != len(AllowedGold) {
		t.Fatal(got)
	}
	got := PickGold([]hub.Source{{ID: "xau", URL: AllowedGold["xau"]}, {ID: "xau", URL: "https://evil.example/"}, {ID: "cbu", URL: Allowed["cbu"]}})
	if len(got) != 1 || got[0].ID != "xau" {
		t.Fatal(got)
	}
}

func TestGoldTime(t *testing.T) {
	for at, want := range map[string]bool{"2026-11-23T01:59:00Z": false, "2026-11-23T02:00:00Z": true, "2026-11-23T16:05:00Z": true, "2026-11-23T16:06:00Z": false} {
		now, _ := time.Parse(time.RFC3339, at)
		if GoldTime(now) != want {
			t.Fatal(at)
		}
	}
}
