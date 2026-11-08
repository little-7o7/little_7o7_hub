//go:build !windows && !darwin

package tray

import "testing"

func TestRunStop(t *testing.T) {
	done := make(chan struct{})
	go func() {
		Run(Menu{Lang: func() string { return "en" }}, func() { Stop() })
		close(done)
	}()
	<-done
	if label(Menu{}) != words["ru"] || label(Menu{Lang: func() string { return "en" }})[0] != "Open Little Hub" {
		t.Fatal("подписи меню")
	}
}
