//go:build !windows

package disks

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Free — место на диске по пути (statfs).
func Free(path string) (Space, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return Space{}, err
	}
	return Space{Free: uint64(s.Bavail) * uint64(s.Bsize), Total: uint64(s.Blocks) * uint64(s.Bsize)}, nil
}

// Suggest — какие диски можно добавить: на Mac — подключённые тома (/Volumes), кроме системного.
func Suggest() []string {
	var out []string
	entries, _ := os.ReadDir("/Volumes")
	for _, e := range entries {
		p := filepath.Join("/Volumes", e.Name())
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir() {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Default — диск по умолчанию: папка «Документы» пользователя.
func Default() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents")
}
