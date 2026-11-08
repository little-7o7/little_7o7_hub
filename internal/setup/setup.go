// Package setup — установщик из хаба (владелец 2026-11-08: «sdelay ustanovshik. legko mne»). Хаб отдаёт файл
// программы для Windows и дописывает в его конец адрес хаба и код подключения:
//
//	JSON {"hub":"…","code":"…"} · 8 байт длины JSON (big-endian) · 8 байт «L7SETUP1»
//
// Такой файл, запущенный двойным щелчком не с места установки, ставит программу и подключает компьютер — без
// PowerShell. Сама программа — байты до хвоста: на место установки идут только они (те же, что в выпуске).
package setup

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// Magic — последние 8 байт файла с хвостом.
const Magic = "L7SETUP1"

const (
	footer     = 16
	maxPayload = 1024
)

// Info — что хаб дописал в установщик.
type Info struct {
	Hub  string `json:"hub"`
	Code string `json:"code"`
}

// Tail — хвост для файла программы (хаб делает такой же, src/lib/pc-setup.ts).
func Tail(i Info) []byte {
	b, _ := json.Marshal(i)
	out := make([]byte, 0, len(b)+footer)
	out = append(out, b...)
	out = binary.BigEndian.AppendUint64(out, uint64(len(b)))
	return append(out, Magic...)
}

// Read — хвост файла path. ok=false — хвоста нет (обычный файл программы); size — сколько байт сама программа.
func Read(path string) (info Info, size int64, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, 0, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Info{}, 0, false, err
	}
	size = st.Size()
	if size < footer {
		return Info{}, size, false, nil
	}
	foot := make([]byte, footer)
	if _, err := f.ReadAt(foot, size-footer); err != nil {
		return Info{}, size, false, err
	}
	if !bytes.Equal(foot[8:], []byte(Magic)) {
		return Info{}, size, false, nil
	}
	n := binary.BigEndian.Uint64(foot[:8])
	if n == 0 || n > maxPayload || int64(n) > size-footer {
		return Info{}, size, false, nil
	}
	body := make([]byte, n)
	if _, err := f.ReadAt(body, size-footer-int64(n)); err != nil && err != io.EOF {
		return Info{}, size, false, err
	}
	if err := json.Unmarshal(body, &info); err != nil || !strings.HasPrefix(info.Hub, "https://") || info.Code == "" {
		return Info{}, size, false, nil
	}
	return info, size - footer - int64(n), true, nil
}
