// Package copyfmt — копии баз под паролем копий: «L7-DB-1» (хаб, little_7o7/src/lib/copy-seal.ts) и «SARPO-DB-1»
// (Sarpo, sarpobusiness/src/lib/pc-backup.ts). Формат один:
//
//	метка | соль 16 | повторы PBKDF2 (4 байта, старший первым) | IV 16 | AES-256-CBC (PKCS7) | HMAC-SHA256 всего перед ним
//
// Ключи — PBKDF2-SHA256(пароль, соль, повторы, 64): первые 32 байта шифруют, последние — HMAC. Программа пароля
// не знает и копию не открывает: она проверяет только заголовок и SHA-256. Открывает — «Расшифровать…» в окне.
package copyfmt

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

var (
	MagicHub   = []byte("L7-DB-1\n")
	MagicSarpo = []byte("SARPO-DB-1\n")
	magics     = [][]byte{MagicHub, MagicSarpo}
)

var (
	ErrNotCopy  = errors.New("это не копия Little Hub или Sarpo")
	ErrPassword = errors.New("пароль не подходит или файл повреждён")
)

const (
	minIter = 1000
	maxIter = 10_000_000
	// MaxPlain — больше не распаковываем (защита от «бомбы»).
	MaxPlain = 2 << 30
)

func magicOf(b []byte) []byte {
	for _, m := range magics {
		if bytes.HasPrefix(b, m) {
			return m
		}
	}
	return nil
}

// Check — похоже ли на целую копию: метка, разумные повторы, длина кратна блоку AES. Пароль не нужен.
func Check(b []byte) error {
	m := magicOf(b)
	if m == nil {
		return ErrNotCopy
	}
	head := len(m) + 16 + 4 + 16
	if len(b) < head+16+32 || (len(b)-head-32)%aes.BlockSize != 0 {
		return ErrNotCopy
	}
	n := binary.BigEndian.Uint32(b[len(m)+16:])
	if n < minIter || n > maxIter {
		return ErrNotCopy
	}
	return nil
}

// Open — расшифровать паролем. Внутри gzip (так делают хаб и Sarpo) — распаковываем.
func Open(b []byte, password string) ([]byte, error) {
	if err := Check(b); err != nil {
		return nil, err
	}
	m := magicOf(b)
	salt := b[len(m) : len(m)+16]
	iter := int(binary.BigEndian.Uint32(b[len(m)+16:]))
	iv := b[len(m)+20 : len(m)+36]
	keys, err := pbkdf2.Key(sha256.New, password, salt, iter, 64)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, keys[32:])
	mac.Write(b[:len(b)-32])
	if !hmac.Equal(mac.Sum(nil), b[len(b)-32:]) {
		return nil, ErrPassword
	}
	block, err := aes.NewCipher(keys[:32])
	if err != nil {
		return nil, err
	}
	body := append([]byte(nil), b[len(m)+36:len(b)-32]...)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(body, body)
	pad := int(body[len(body)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(body) || !bytes.Equal(body[len(body)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, ErrPassword
	}
	body = body[:len(body)-pad]
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		out, err := io.ReadAll(io.LimitReader(zr, MaxPlain+1))
		if err != nil {
			return nil, err
		}
		if len(out) > MaxPlain {
			return nil, errors.New("копия слишком большая")
		}
		return out, nil
	}
	return body, nil
}
