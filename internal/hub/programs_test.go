package hub

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// «Программы»: файл с хаба — с ключом компьютера, переадресация на GitHub его не уносит; прямая ссылка на выпуск
// плагина — без ключа, переадресации только на GitHub по https; чужая ссылка — мимо.

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func answer(code int, body string, hdr map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func TestProgramFile(t *testing.T) {
	ctx := context.Background()
	var seen []string
	auth := map[string]string{}
	jump := "https://objects.githubusercontent.com/x?asset=21"
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		seen = append(seen, u)
		auth[u] = r.Header.Get("Authorization")
		switch {
		case u == "https://hub.example/api/pc/programs":
			return answer(200, `{"programs":[{"dir":"Little Hub","name":"a.exe","url":"/api/pc/asset/21","size":4}]}`, nil), nil
		case u == "https://hub.example/api/pc/asset/21":
			return answer(302, "", map[string]string{"Location": jump}), nil
		case u == jump:
			return answer(200, "MZ21", nil), nil
		case u == PluginReleases+"v1/S.exe":
			return answer(302, "", map[string]string{"Location": "https://release-assets.githubusercontent.com/s?x=1"}), nil
		case u == "https://release-assets.githubusercontent.com/s?x=1":
			return answer(200, "MZ-plugin", nil), nil
		case u == PluginReleases+"v1/evil.exe":
			return answer(302, "", map[string]string{"Location": "https://evil.example/x"}), nil
		case u == PluginReleases+"v1/plain.exe":
			return answer(302, "", map[string]string{"Location": "http://objects.githubusercontent.com/x"}), nil
		case u == "https://hub.example/api/pc/programs/sarpo":
			return answer(404, `{"error":"нет"}`, nil), nil
		}
		return answer(500, "", nil), nil
	})
	c := &Client{Base: "https://hub.example", Token: token, Version: "1.3.0", HTTP: &http.Client{Transport: tr}}
	list, err := c.Programs(ctx)
	if err != nil || len(list) != 1 || list[0].URL != "/api/pc/asset/21" {
		t.Fatal(list, err)
	}
	var b bytes.Buffer
	if n, err := c.ProgramFile(ctx, list[0], &b); err != nil || n != 4 || b.String() != "MZ21" {
		t.Fatal(n, err, b.String())
	}
	if auth["https://hub.example/api/pc/asset/21"] != "Bearer "+token || auth[jump] != "" {
		t.Fatal("ключ компьютера ушёл не туда", auth)
	}
	b.Reset()
	if _, err := c.ProgramFile(ctx, Program{URL: PluginReleases + "v1/S.exe"}, &b); err != nil || b.String() != "MZ-plugin" {
		t.Fatal(err, b.String())
	}
	if auth[PluginReleases+"v1/S.exe"] != "" {
		t.Fatal("ключ компьютера ушёл на GitHub")
	}
	for _, p := range []Program{
		{URL: PluginReleases + "v1/evil.exe"},  // переадресация на чужой сайт
		{URL: PluginReleases + "v1/plain.exe"}, // переадресация без https
		{URL: "https://evil.example/x.exe"},
		{URL: "/api/pc/copy"},
		{URL: "https://github.com/other/repo/releases/download/v1/x.exe"},
		{URL: "/api/pc/programs/sarpo"}, // 404
		{URL: "/api/pc/asset/../copy"},
		{URL: "/api/pc/asset/1/../../copy"},
		{URL: "/api/pc/asset/%2e%2e/copy"},
		{URL: PluginReleases + "../../../other/repo/releases/download/v1/x.exe"},
	} {
		seen = nil
		if _, err := c.ProgramFile(ctx, p, io.Discard); err == nil {
			t.Fatal("принята", p.URL)
		}
		for _, u := range seen {
			if strings.Contains(u, "evil.example") || strings.HasPrefix(u, "http://") || strings.Contains(u, "/api/pc/copy") || strings.Contains(u, "other/repo") {
				t.Fatal("сходил по чужой ссылке", u)
			}
		}
	}

	// Хаб старше: «Программ» нет — ErrNothing.
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	old := &Client{Base: srv.URL, Token: token, Version: "1.3.0"}
	if _, err := old.Programs(ctx); err != ErrNothing {
		t.Fatal(err)
	}
}
