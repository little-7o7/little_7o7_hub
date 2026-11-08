//go:build darwin

package secret

import (
	"os/exec"
	"strings"
)

const (
	service = "Little Hub"
	account = "hub-token"
)

type keychain struct{}

// New — ключ в «Связке ключей» пользователя (через /usr/bin/security: она же в списке доступа к записи, и macOS
// не спрашивает разрешения при каждом запуске).
func New(string) Store { return keychain{} }

func (keychain) Get() (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
			return "", ErrNone
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (keychain) Set(token string) error {
	return exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", service, "-a", account, "-l", "Little Hub — ключ компьютера", "-w", token).Run()
}

func (keychain) Delete() error {
	err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", account).Run()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
		return nil
	}
	return err
}
