// Package fileseal — файлы проектов на диске под паролем копий (план pc-backup-2, решение 6; владелец 2026-11-08,
// пункт 109: «шифровать всё… на случай кражи компьютера»). Программа пароля не знает: хаб выводит из пароля копий
// пару ключей X25519 и отдаёт программе только открытый (little_7o7/src/lib/file-seal.ts). Зашифровать файл может
// программа, открыть — только тот, кто знает пароль (окно «Файлы»).
//
//	«L7-F-1\n» | соль 16 | повторы PBKDF2 (4 байта, старший первым) | открытый ключ 32 | ключ файла X25519 32 |
//	куски AES-256-GCM по 64 КБ (последний короче или пустой), у каждого 16 байт проверки
//
// Ключи: k = PBKDF2-SHA256(пароль, соль, повторы, 64) — тот же, что у копий баз (L7-DB-1); закрытый X25519 =
// HKDF-SHA256(k, —, "L7-F-1 x25519"). У каждого файла свой ключ: X25519(свой, открытый) → HKDF-SHA256(…, свой
// открытый ‖ открытый, "L7-F-1 file"). Номер куска и «последний» — в nonce: обрезанный или переставленный файл не
// откроется; заголовок проверяется вместе с каждым куском.
package fileseal

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	// Ext — зашифрованный файл на диске: «Магазин.xlsx.l7».
	Ext     = ".l7"
	Chunk   = 64 << 10
	tagSize = 16
	// HeadLen — заголовок файла.
	HeadLen = len(magic) + 16 + 4 + 32 + 32
	minIter = 1000
	maxIter = 10_000_000
)

const magic = "L7-F-1\n"

var (
	ErrNotSealed = errors.New("это не файл Little Hub под паролем")
	ErrPassword  = errors.New("пароль не подходит")
	ErrDamaged   = errors.New("файл повреждён")
)

// Lock — открытый ключ из пароля копий (от хаба): им программа шифрует файлы.
type Lock struct {
	Pub  []byte `json:"pub"`
	Salt []byte `json:"salt"`
	Iter uint32 `json:"iterations"`
}

// Valid — ключ похож на настоящий.
func (l Lock) Valid() error {
	if len(l.Pub) != 32 || len(l.Salt) != 16 || l.Iter < minIter || l.Iter > maxIter {
		return errors.New("неверный ключ шифрования файлов")
	}
	if _, err := ecdh.X25519().NewPublicKey(l.Pub); err != nil {
		return errors.New("неверный ключ шифрования файлов")
	}
	return nil
}

// Tag — короткая метка ключа (сменили пароль копий — другая метка).
func (l Lock) Tag() string {
	if len(l.Pub) < 6 {
		return ""
	}
	return hex.EncodeToString(l.Pub[:6])
}

// Private — закрытый ключ из пароля (окно «Файлы»): пароль, соль и повторы из заголовка файла.
func Private(password string, salt []byte, iter uint32) (*ecdh.PrivateKey, error) {
	if len(salt) != 16 || iter < minIter || iter > maxIter {
		return nil, ErrNotSealed
	}
	k, err := pbkdf2.Key(sha256.New, password, salt, int(iter), 64)
	if err != nil {
		return nil, err
	}
	sk, err := hkdf.Key(sha256.New, k, nil, "L7-F-1 x25519", 32)
	if err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPrivateKey(sk)
}

// LockOf — открытый ключ из пароля (проверки и образцы; в работе его присылает хаб).
func LockOf(password string, salt []byte, iter uint32) (Lock, error) {
	p, err := Private(password, salt, iter)
	if err != nil {
		return Lock{}, err
	}
	return Lock{Pub: p.PublicKey().Bytes(), Salt: append([]byte(nil), salt...), Iter: iter}, nil
}

func fileKey(shared, eph, pub []byte) (cipher.AEAD, error) {
	k, err := hkdf.Key(sha256.New, shared, append(append([]byte(nil), eph...), pub...), "L7-F-1 file", 32)
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func nonce(n uint32, last bool) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b[7:11], n)
	if last {
		b[11] = 1
	}
	return b
}

// Size — сколько займёт файл из n байт.
func Size(n int64) int64 {
	chunks := max((n+Chunk-1)/Chunk, 1)
	return int64(HeadLen) + n + chunks*tagSize
}

type writer struct {
	w    io.Writer
	aead cipher.AEAD
	head []byte
	buf  []byte
	n    uint32
	err  error
	done bool
}

// NewWriter — зашифровать всё, что пишут в него, в w. Close обязателен: он пишет последний кусок.
func NewWriter(w io.Writer, l Lock) (io.WriteCloser, error) {
	if err := l.Valid(); err != nil {
		return nil, err
	}
	pub, _ := ecdh.X25519().NewPublicKey(l.Pub)
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, err
	}
	aead, err := fileKey(shared, eph.PublicKey().Bytes(), l.Pub)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 0, HeadLen)
	head = append(head, magic...)
	head = append(head, l.Salt...)
	head = binary.BigEndian.AppendUint32(head, l.Iter)
	head = append(head, l.Pub...)
	head = append(head, eph.PublicKey().Bytes()...)
	if _, err := w.Write(head); err != nil {
		return nil, err
	}
	return &writer{w: w, aead: aead, head: head, buf: make([]byte, 0, Chunk)}, nil
}

func (s *writer) flush(last bool) error {
	if s.n == ^uint32(0) {
		return errors.New("файл слишком большой")
	}
	out := s.aead.Seal(nil, nonce(s.n, last), s.buf, s.head)
	s.n++
	s.buf = s.buf[:0]
	_, err := s.w.Write(out)
	return err
}

func (s *writer) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.done {
		return 0, errors.New("файл уже закрыт")
	}
	n := 0
	for len(p) > 0 {
		// Полный кусок пишем, только когда за ним есть ещё данные: так последний кусок всегда помечен «последний».
		if len(s.buf) == Chunk {
			if s.err = s.flush(false); s.err != nil {
				return n, s.err
			}
		}
		k := copy(s.buf[len(s.buf):Chunk], p)
		s.buf = s.buf[:len(s.buf)+k]
		p = p[k:]
		n += k
	}
	return n, nil
}

func (s *writer) Close() error {
	if s.err != nil || s.done {
		return s.err
	}
	s.done = true
	s.err = s.flush(true)
	return s.err
}

// Bytes — зашифровать data целиком.
func Bytes(data []byte, l Lock) ([]byte, error) {
	var b bytes.Buffer
	b.Grow(int(Size(int64(len(data)))))
	w, err := NewWriter(&b, l)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Head — что в заголовке: соль, повторы и открытый ключ (каким паролем закрыт файл).
func Head(b []byte) (Lock, error) {
	if len(b) < HeadLen || string(b[:len(magic)]) != magic {
		return Lock{}, ErrNotSealed
	}
	i := len(magic)
	l := Lock{Salt: append([]byte(nil), b[i:i+16]...), Iter: binary.BigEndian.Uint32(b[i+16:]), Pub: append([]byte(nil), b[i+20:i+52]...)}
	if l.Iter < minIter || l.Iter > maxIter {
		return Lock{}, ErrNotSealed
	}
	return l, nil
}

// IsSealed — начинается ли файл с метки L7-F-1.
func IsSealed(head []byte) bool { return bytes.HasPrefix(head, []byte(magic)) }

// Keys — закрытые ключи из одного пароля для разных солей (файлы под прежним паролем копий открываются прежним
// паролем). PBKDF2 — доли секунды на каждую соль, поэтому помним.
type Keys struct {
	Password string
	mu       sync.Mutex
	m        map[string]*ecdh.PrivateKey
}

// For — закрытый ключ для файла с этим заголовком; пароль не тот — ErrPassword.
func (k *Keys) For(l Lock) (*ecdh.PrivateKey, error) {
	id := hex.EncodeToString(l.Salt) + fmt.Sprint(l.Iter) + hex.EncodeToString(l.Pub)
	k.mu.Lock()
	defer k.mu.Unlock()
	if p, ok := k.m[id]; ok {
		return p, nil
	}
	p, err := Private(k.Password, l.Salt, l.Iter)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(p.PublicKey().Bytes(), l.Pub) {
		return nil, ErrPassword
	}
	if k.m == nil {
		k.m = map[string]*ecdh.PrivateKey{}
	}
	k.m[id] = p
	return p, nil
}

type reader struct {
	r    *bufio.Reader
	aead cipher.AEAD
	head []byte
	buf  []byte
	out  []byte
	n    uint32
	done bool
	err  error
}

// NewReader — расшифровать файл из r ключами keys.
func NewReader(r io.Reader, keys *Keys) (io.Reader, error) {
	br := bufio.NewReaderSize(r, Chunk+tagSize+1)
	head := make([]byte, HeadLen)
	if _, err := io.ReadFull(br, head); err != nil {
		return nil, ErrNotSealed
	}
	l, err := Head(head)
	if err != nil {
		return nil, err
	}
	priv, err := keys.For(l)
	if err != nil {
		return nil, err
	}
	eph, err := ecdh.X25519().NewPublicKey(head[HeadLen-32:])
	if err != nil {
		return nil, ErrDamaged
	}
	shared, err := priv.ECDH(eph)
	if err != nil {
		return nil, ErrDamaged
	}
	aead, err := fileKey(shared, eph.Bytes(), l.Pub)
	if err != nil {
		return nil, err
	}
	return &reader{r: br, aead: aead, head: head, buf: make([]byte, Chunk+tagSize)}, nil
}

func (s *reader) next() error {
	n, err := io.ReadFull(s.r, s.buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	last := n < len(s.buf)
	if !last {
		if _, perr := s.r.Peek(1); errors.Is(perr, io.EOF) {
			last = true
		}
	}
	if n < tagSize {
		return ErrDamaged
	}
	plain, oerr := s.aead.Open(s.buf[:0:0], nonce(s.n, last), s.buf[:n], s.head)
	if oerr != nil {
		return ErrDamaged
	}
	s.n++
	s.out, s.done = plain, last
	return nil
}

func (s *reader) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		if s.done {
			return 0, io.EOF
		}
		if s.err = s.next(); s.err != nil {
			return 0, s.err
		}
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

// Open — расшифровать файл целиком (небольшие файлы: Excel, фото).
func Open(b []byte, keys *Keys) ([]byte, error) {
	r, err := NewReader(bytes.NewReader(b), keys)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
