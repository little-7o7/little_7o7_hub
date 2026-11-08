package app

import (
	"testing"
	"time"
)

func TestDebounce(t *testing.T) {
	d := Debounce{Quiet: 2 * time.Minute, Max: 15 * time.Minute}
	t0 := time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	if d.Observe("1", "1", at(0)) {
		t.Fatal("нет изменений")
	}
	if d.Observe("2", "1", at(0)) || !d.Waiting().Equal(at(0)) {
		t.Fatal("первое изменение — ждём")
	}
	if !d.Observe("2", "1", at(2)) {
		t.Fatal("2 минуты тихо — копия")
	}
	d.Reset()
	// Правки каждые 2 минуты — копия не позже 15 минут от первой.
	v := 10
	got := -1
	for m := 0; m <= 20; m += 2 {
		v++
		if d.Observe(string(rune('a'+v)), "1", at(m)) {
			got = m
			break
		}
	}
	if got != 16 && got != 14 {
		t.Fatalf("копия на %d-й минуте", got)
	}
	d.Reset()
	if d.Observe("", "1", at(30)) || !d.Waiting().IsZero() {
		t.Fatal("пустой флаг — не ждём")
	}
}
