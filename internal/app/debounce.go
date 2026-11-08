package app

import "time"

// Debounce — когда брать копию после изменения (план pc-backup-1, решение 9): флаг хаба изменился — ждём 2 минуты
// затишья (флаг не меняется), но не дольше 15 минут от первого изменения: так пачка правок даёт одну копию,
// а долгая работа в хабе всё равно сохраняется каждые 15 минут.
type Debounce struct {
	Quiet, Max  time.Duration
	first, last time.Time
	seen        string
}

// Observe — флаг v при уже сохранённой копии с отпечатком copied. true — пора брать копию.
func (d *Debounce) Observe(v, copied string, now time.Time) bool {
	if v == "" || v == copied {
		d.Reset()
		d.seen = v
		return false
	}
	if d.first.IsZero() {
		d.first, d.last, d.seen = now, now, v
		return false
	}
	if v != d.seen {
		d.seen, d.last = v, now
	}
	return now.Sub(d.last) >= d.Quiet || now.Sub(d.first) >= d.Max
}

// Reset — копия взята.
func (d *Debounce) Reset() { d.first, d.last = time.Time{}, time.Time{} }

// Waiting — с какого времени ждём копию (для окна); ноль — не ждём.
func (d *Debounce) Waiting() time.Time { return d.first }
