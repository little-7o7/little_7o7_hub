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
