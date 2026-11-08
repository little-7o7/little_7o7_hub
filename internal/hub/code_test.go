package hub

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// «Код»: список и Excel — с хаба с ключом компьютера; архив — по переадресации хаба только на https codeload.github.com
// и без ключа; чужой адрес — мимо.

func TestCode(t *testing.T) {
	ctx := context.Background()
	auth := map[string]string{}
	var seen []string
	zip := "https://codeload.github.com/little-7o7/sarpobusiness/legacy.zip/refs/heads/master?token=T"
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		seen = append(seen, u)
		auth[u] = r.Header.Get("Authorization")
		switch u {
		case "https://hub.example/api/pc/code":
			return answer(200, `{"on":true,"repos":[{"name":"sarpobusiness","branch":"master","pushedAt":"2026-11-08T10:00:00Z"}]}`, nil), nil
		case "https://hub.example/api/pc/code/sarpobusiness/zip":
			return answer(302, "", map[string]string{"Location": zip}), nil
		case zip:
			return answer(200, "PK-code", nil), nil
		case "https://hub.example/api/pc/code/sarpobusiness/excel":
			return answer(200, "xlsx", nil), nil
		case "https://hub.example/api/pc/code/evil/zip":
			return answer(302, "", map[string]string{"Location": "https://evil.example/x.zip"}), nil
		case "https://hub.example/api/pc/code/plain/zip":
			return answer(302, "", map[string]string{"Location": "http://codeload.github.com/x.zip"}), nil
		case "https://hub.example/api/pc/code/port/zip":
			return answer(302, "", map[string]string{"Location": "https://codeload.github.com:8443/x.zip"}), nil
		case "https://hub.example/api/pc/code/jump/zip":
			return answer(302, "", map[string]string{"Location": "https://codeload.github.com/jump.zip"}), nil
		case "https://codeload.github.com/jump.zip":
			return answer(302, "", map[string]string{"Location": "https://evil.example/y.zip"}), nil
		case "https://hub.example/api/pc/code/gone/zip":
			return answer(404, `{"error":"нет"}`, nil), nil
		case "https://hub.example/api/pc/code/down/excel":
			return answer(502, `{"error":"GitHub не ответил"}`, nil), nil
		}
		return answer(500, "", nil), nil
	})
	c := &Client{Base: "https://hub.example", Token: token, Version: "1.3.0", HTTP: &http.Client{Transport: tr}}
	repos, err := c.Code(ctx)
	if err != nil || len(repos) != 1 || repos[0].Branch != "master" || repos[0].PushedAt == "" {
		t.Fatal(repos, err)
	}
	var b bytes.Buffer
	if _, err := c.CodeFile(ctx, "sarpobusiness", "zip", &b, MaxCode); err != nil || b.String() != "PK-code" {
		t.Fatal(err, b.String())
	}
	if auth["https://hub.example/api/pc/code/sarpobusiness/zip"] != "Bearer "+token || auth[zip] != "" {
		t.Fatal("ключ компьютера ушёл не туда", auth)
	}
	b.Reset()
	if _, err := c.CodeFile(ctx, "sarpobusiness", "excel", &b, 100); err != nil || b.String() != "xlsx" {
		t.Fatal(err, b.String())
	}
	if _, err := c.CodeFile(ctx, "sarpobusiness", "excel", io.Discard, 2); err == nil {
		t.Fatal("больше max — принят")
	}
	for _, repo := range []string{"evil", "plain", "port", "jump"} {
		seen = nil
		if _, err := c.CodeFile(ctx, repo, "zip", io.Discard, MaxCode); err == nil {
			t.Fatal("принят", repo)
		}
		for _, u := range seen {
			if strings.Contains(u, "evil.example") || strings.HasPrefix(u, "http://") || strings.Contains(u, ":8443") {
				t.Fatal("сходил по чужой ссылке", u)
			}
		}
	}
	if _, err := c.CodeFile(ctx, "gone", "zip", io.Discard, MaxCode); !errors.Is(err, ErrNothing) {
		t.Fatal(err)
	}
	if _, err := c.CodeFile(ctx, "down", "excel", io.Discard, MaxCode); !IsAPI(err) {
		t.Fatal(err)
	}
	seen = nil
	for _, bad := range [][2]string{{"..", "zip"}, {"a/b", "zip"}, {"x", "tar"}, {"", "zip"}} {
		if _, err := c.CodeFile(ctx, bad[0], bad[1], io.Discard, MaxCode); !IsAPI(err) {
			t.Fatal("принято", bad, err)
		}
	}
	if len(seen) != 0 {
		t.Fatal("с чужим именем сходил на хаб", seen)
	}

	// Ключа GitHub в хабе нет — пусто; хаб старше — ErrNothing.
	off := &Client{Base: "https://hub.example", Token: token, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return answer(200, `{"on":false,"repos":[]}`, nil), nil
	})}}
	if repos, err := off.Code(ctx); err != nil || repos != nil {
		t.Fatal(repos, err)
	}
	old := &Client{Base: "https://hub.example", Token: token, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return answer(404, "", nil), nil
	})}}
	if _, err := old.Code(ctx); !errors.Is(err, ErrNothing) {
		t.Fatal(err)
	}
}
