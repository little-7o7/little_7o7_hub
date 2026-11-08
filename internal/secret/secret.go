// Package secret — ключ компьютера (выдан хабом при подключении). Windows — DPAPI (расшифровать может только этот
// пользователь на этом компьютере), Mac — «Связка ключей». В config.json ключа нет.
package secret

import "errors"

// ErrNone — ключа ещё нет (компьютер не подключён).
var ErrNone = errors.New("компьютер не подключён")

// Store — где лежит ключ. dir — папка настроек программы.
type Store interface {
	Get() (string, error)
	Set(token string) error
	Delete() error
}
