package fileseal

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

// Образец: тот же ключ выводит хаб (little_7o7/tests/file-seal.test.ts) — пароль, соль 00…0f, 1000 повторов.
const samplePub = "a9f9d47038769d205c38f4b33a36844aad74787c08c0192e9cdc39599c9ab132"

func sample(t *testing.T) Lock {
	t.Helper()
	salt, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	l, err := LockOf("пароль-копий-2026", salt, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSamePubAsHub(t *testing.T) {
	l := sample(t)
	if hex.EncodeToString(l.Pub) != samplePub || l.Valid() != nil || l.Tag() != samplePub[:12] {
		t.Fatal(hex.EncodeToString(l.Pub))
	}
}

func TestRoundTrip(t *testing.T) {
	l := sample(t)
	keys := &Keys{Password: "пароль-копий-2026"}
	for _, n := range []int{0, 1, 100, Chunk - 1, Chunk, Chunk + 1, 3*Chunk + 7, 2 * Chunk} {
		data := bytes.Repeat([]byte{byte(n)}, n)
		for i := range data {
			data[i] = byte(i * 7)
		}
		b, err := Bytes(data, l)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(b)) != Size(int64(n)) || !IsSealed(b) {
			t.Fatal("размер", n, len(b), Size(int64(n)))
		}
		got, err := Open(b, keys)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatal("не открылся", n, err)
		}
		// Читаем маленькими кусками — то же самое.
		r, _ := NewReader(bytes.NewReader(b), keys)
		var out bytes.Buffer
		buf := make([]byte, 999)
		for {
			k, err := r.Read(buf)
			out.Write(buf[:k])
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(out.Bytes(), data) {
			t.Fatal("по кускам", n)
		}
	}
}

func TestStreamWriterSmallWrites(t *testing.T) {
	l := sample(t)
	var b bytes.Buffer
	w, err := NewWriter(&b, l)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 2*Chunk+5)
	for i := range data {
		data[i] = byte(i)
	}
	for i := 0; i < len(data); i += 1000 {
		_, _ = w.Write(data[i:min(i+1000, len(data))])
	}
	if err := w.Close(); err != nil || w.Close() != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("запись после Close")
	}
	got, err := Open(b.Bytes(), &Keys{Password: "пароль-копий-2026"})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal(err)
	}
}

func TestDamagedAndWrongPassword(t *testing.T) {
	l := sample(t)
	data := make([]byte, 2*Chunk+10)
	b, _ := Bytes(data, l)
	keys := &Keys{Password: "пароль-копий-2026"}
	if _, err := Open(b, &Keys{Password: "не тот пароль"}); !errors.Is(err, ErrPassword) {
		t.Fatal("чужой пароль", err)
	}
	// Обрезан по границе куска: первые два куска (не «последние») — не откроется.
	cut := b[:HeadLen+2*(Chunk+tagSize)]
	if _, err := Open(cut, keys); !errors.Is(err, ErrDamaged) {
		t.Fatal("обрезанный", err)
	}
	// Испорчен байт в середине.
	bad := append([]byte(nil), b...)
	bad[HeadLen+Chunk+50] ^= 1
	if _, err := Open(bad, keys); !errors.Is(err, ErrDamaged) {
		t.Fatal("испорченный", err)
	}
	// Подменён заголовок (повторы) — проверка куска не сходится.
	bad = append([]byte(nil), b...)
	bad[HeadLen-1] ^= 1
	if _, err := Open(bad, keys); err == nil {
		t.Fatal("подменённый ключ файла")
	}
	// Куски переставлены.
	sw := append([]byte(nil), b[:HeadLen]...)
	sw = append(sw, b[HeadLen+Chunk+tagSize:HeadLen+2*(Chunk+tagSize)]...)
	sw = append(sw, b[HeadLen:HeadLen+Chunk+tagSize]...)
	sw = append(sw, b[HeadLen+2*(Chunk+tagSize):]...)
	if _, err := Open(sw, keys); !errors.Is(err, ErrDamaged) {
		t.Fatal("переставленные", err)
	}
	// Хвост после последнего куска не прочитается как часть файла: пустой лишний кусок — ошибка.
	if _, err := Open(append(append([]byte(nil), b...), make([]byte, 3)...), keys); err == nil {
		t.Fatal("лишний хвост")
	}
	for _, x := range [][]byte{nil, []byte("L7-DB-1\n"), b[:HeadLen-1], b[:HeadLen]} {
		if _, err := Open(x, keys); err == nil {
			t.Fatal("не файл", len(x))
		}
	}
	if _, err := Head([]byte("xx")); !errors.Is(err, ErrNotSealed) {
		t.Fatal(err)
	}
	if (Lock{}).Valid() == nil || (Lock{Pub: make([]byte, 32), Salt: make([]byte, 16), Iter: 10}).Valid() == nil {
		t.Fatal("неверный ключ принят")
	}
	if _, err := NewWriter(io.Discard, Lock{}); err == nil {
		t.Fatal("писать без ключа")
	}
	if _, err := Private("x", []byte("short"), 1000); err == nil {
		t.Fatal("короткая соль")
	}
	// Прежний пароль копий: файл открывается им, ключ помним.
	other, _ := LockOf("старый-пароль-копий", l.Salt, 1000)
	ob, _ := Bytes([]byte("old"), other)
	old := &Keys{Password: "старый-пароль-копий"}
	for i := 0; i < 2; i++ {
		if got, err := Open(ob, old); err != nil || string(got) != "old" {
			t.Fatal(err)
		}
	}
	if len(old.m) != 1 {
		t.Fatal("ключ не запомнен")
	}
}
