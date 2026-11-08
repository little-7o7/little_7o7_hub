//go:build ignore

// Поддельный хаб для ручной проверки программы: go run tools/fakehub/main.go 127.0.0.1:47700
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	copyFile, _ := os.ReadFile("internal/copyfmt/testdata/hub.l7copy")
	ver := "1"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pc/pair", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		log.Printf("pair %v", b)
		if b["code"] != "ABCD1234ABCD1234" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "id": 1, "name": b["name"], "flag": "/api/pc/flag/k"})
	})
	mux.HandleFunc("GET /api/pc/flag/k", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"v": ver})
	})
	mux.HandleFunc("GET /api/pc/copy", func(w http.ResponseWriter, r *http.Request) {
		s := sha256.Sum256(copyFile)
		w.Header().Set("X-Copy-Day", time.Now().In(time.FixedZone("T", 5*3600)).Format("2006-01-02"))
		w.Header().Set("X-Copy-Version", ver)
		w.Header().Set("X-Copy-Sha256", hex.EncodeToString(s[:]))
		_, _ = w.Write(copyFile)
	})
	mux.HandleFunc("POST /api/pc/status", func(w http.ResponseWriter, r *http.Request) {
		var b any
		_ = json.NewDecoder(r.Body).Decode(&b)
		j, _ := json.Marshal(b)
		log.Printf("status %s", j)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /api/pc/rates/sources", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"sources":[]}`)) })
	mux.HandleFunc("POST /api/pc/rates", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"source":"cbu","ok":true,"day":"2026-11-08"},{"source":"bankuz","ok":true,"day":"2026-11-08"},{"source":"nbu","ok":false,"error":"источник ответил 403"}]}`))
	})
	mux.HandleFunc("GET /api/pc/version", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	log.Fatal(http.ListenAndServe(os.Args[1], mux))
}
