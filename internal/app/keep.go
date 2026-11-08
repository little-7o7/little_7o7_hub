package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/little-7o7/little_7o7_hub/internal/config"
	"github.com/little-7o7/little_7o7_hub/internal/copyfmt"
	"github.com/little-7o7/little_7o7_hub/internal/days"
	"github.com/little-7o7/little_7o7_hub/internal/disks"
	"github.com/little-7o7/little_7o7_hub/internal/hub"
)

// Рядом с копиями (пункты 82–84, 86): журнал и настройки программы в «Little Hub/Программа», «Как восстановить.txt»
// в «Little Hub», проверка копий баз раз в неделю.

const (
	ProgramFolder = "Программа"
	SettingsFile  = "Настройки.json"
	RestoreFile   = "Как восстановить.txt"
	CheckEvery    = 7 * 24 * time.Hour
)

// Saved — настройки программы на диске (без ключа компьютера): после переустановки диски и язык берутся отсюда.
type Saved struct {
	Hub   string   `json:"hub"`
	Name  string   `json:"name"`
	Disks []string `json:"disks"`
	Lang  string   `json:"lang,omitempty"`
	At    string   `json:"at"`
}

// writeIfChanged — записать, только если содержимое другое (диск не трогаем зря).
func writeIfChanged(path string, b []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// keepProgram — журнал, настройки и «Как восстановить.txt» на диске копий (после каждой полной раскладки).
func (a *App) keepProgram(root string) {
	dir := filepath.Join(root, ProgramFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if a.Dir != "" {
		if b, err := os.ReadFile(filepath.Join(a.Dir, "littlehub.log")); err == nil {
			_ = writeIfChanged(filepath.Join(dir, "Журнал.txt"), b)
		}
	}
	cfg := a.Cfg.Get()
	s := Saved{Hub: cfg.Hub, Name: cfg.Name, Disks: cfg.Disks, Lang: cfg.Lang}
	path := filepath.Join(dir, SettingsFile)
	if old, err := os.ReadFile(path); err != nil || !sameSaved(old, s) {
		s.At = a.now().UTC().Format(time.RFC3339)
		if b, err := json.MarshalIndent(s, "", "  "); err == nil {
			_ = writeIfChanged(path, b)
		}
	}
	_ = writeIfChanged(filepath.Join(root, RestoreFile), restoreText())
}

// sameSaved — в файле те же настройки (время не в счёт).
func sameSaved(old []byte, s Saved) bool {
	var prev Saved
	if json.Unmarshal(old, &prev) != nil {
		return false
	}
	prev.At, s.At = "", ""
	x, _ := json.Marshal(prev)
	y, _ := json.Marshal(s)
	return bytes.Equal(x, y)
}

// findSaved — настройки, оставленные на дисках прежней установкой (новый компьютер, переустановка): самые свежие.
func findSaved(candidates []string) (Saved, string, bool) {
	var best Saved
	from := ""
	for _, d := range candidates {
		b, err := os.ReadFile(filepath.Join(disks.Root(d), ProgramFolder, SettingsFile))
		if err != nil {
			continue
		}
		var s Saved
		if json.Unmarshal(b, &s) != nil || len(s.Disks) == 0 || len(s.Disks) > 20 {
			continue
		}
		if from == "" || s.At > best.At {
			best, from = s, d
		}
	}
	return best, from, from != ""
}

// restoreSaved — при подключении без дисков: взять диски и язык с диска прежней установки (пункт 83).
func (a *App) restoreSaved(cfg *config.Config) {
	if len(cfg.Disks) > 0 {
		return
	}
	cands := append(disks.Suggest(), disks.Default())
	if a.Candidates != nil {
		cands = a.Candidates()
	}
	s, from, ok := findSaved(cands)
	if !ok {
		return
	}
	var list []string
	for _, d := range s.Disks {
		if c := disks.Clean(d); c != "" {
			list = append(list, c)
		}
	}
	if len(list) == 0 {
		return
	}
	cfg.Disks = list
	if s.Lang == "ru" || s.Lang == "en" {
		cfg.Lang = s.Lang
	}
	a.restoredFrom = from
}

// verify — раз в неделю перечитать копии баз (пункт 86): SHA-256 не сходится с записанным или файл не похож на
// копию — удалить, докачка возьмёт его заново. Перед докачкой.
func (a *App) verify(root string, list []hub.FileProject) {
	cfg := a.Cfg.Get()
	if t, err := time.Parse(time.RFC3339, cfg.CheckedAt); err == nil && a.now().Sub(t) < CheckEvery {
		return
	}
	n := 0
	var broken []string
	for _, p := range copyProjects(list) {
		k, bad := days.Verify(root, p, copyfmt.HeadLen, copyfmt.CheckHead)
		n += k
		for _, d := range bad {
			broken = append(broken, p.Folder+" "+d)
		}
	}
	_ = a.Cfg.Update(func(x *config.Config) { x.CheckedAt = a.now().UTC().Format(time.RFC3339) })
	if len(broken) > 0 {
		a.Logf(true, "Проверка копий: повреждены и будут скачаны заново — %s", strings.Join(broken, ", "))
		a.Kick() // копия хаба за сегодня — сразу
		return
	}
	if n > 0 {
		a.Logf(false, "Проверка копий: %d копий баз целы", n)
	}
}

// restoreText — «Как восстановить.txt»: по-русски, для человека без программы под рукой.
func restoreText() []byte {
	t := `Little Hub — где что лежит и как всё вернуть

ГДЕ ЧТО ЛЕЖИТ (эта папка «Little Hub»)
• Хаб/Копии базы/<месяц>/hub-<день>.l7copy — копия базы хаба за каждый день, 30 последних дней.
• Sarpo Business/Копии базы/<месяц>/sarpo-<день>.l7copy — копия базы Sarpo за каждый день. Файл сегодняшнего
  дня заменяется после каждого изменения на сайте, в 00:00 по Ташкенту замирает.
• Sarpo Business/<бизнес>/ — Excel с первого дня («что есть сейчас», продажи по дням, история по месяцам,
  прибыль, «Удалённые и отменённые»), Накладные/ — накладная каждой продажи по дням, папки товаров с фото
  (оригиналы, как сняли на телефон), клиентов, поставщиков и мастеров. Архивные и кого больше нет — в папках
  «… (архив)».
• Sarpo Business/<бизнес>/Фото базы/ — чеки операций, паспорта и документы гостей, поломки, QR поставщиков, скрины
  переводов. «Список фото.csv» — что это за фото и где оно лежит в хранилище Sarpo (как в копии базы).
• Strassio/ — ключи, клиенты, оплаты и папка каждого клиента с договорами.
• Старые версии/<день>/… — прежние файлы Excel, 30 дней.
• Программа/ — журнал программы (Журнал.txt) и её настройки (Настройки.json, без ключа компьютера).

ПАРОЛЬ КОПИЙ
Копии баз (.l7copy) и файлы проектов (.l7) зашифрованы паролем копий. Его знаете только вы: без него их не открыть
ни на этом компьютере, ни в хабе, ни у того, кто унесёт диск. Пароль задаётся в хабе: Настройки → Компьютеры →
«Пароль копий». Сменили пароль — старые файлы открываются старым паролем.

ОТКРЫТЬ ФАЙЛ
Окно Little Hub (значок у часов на Windows, в строке меню на Mac) → «Файлы» → файл → пароль копий. Программа
расшифрует его во временную папку и откроет в Excel или просмотре фото; через час временный файл сотрётся.
«Сохранить открытыми» — расшифровать папку целиком: файлы лягут без пароля в «Little Hub — открытые» рядом с этой
папкой (после работы их лучше удалить).

ВЕРНУТЬ БАЗУ
1. Окно Little Hub → «Файлы» → «Копии базы» → нужный день → «Открыть»: получится файл .sql — вся база командами SQL.
   Или «Расшифровать копию»: выбрать файл .l7copy и ввести пароль копий.
2. Базу возвращает тот, кто ведёт проект: сделать копию текущей базы, затем загрузить файл в пустую базу PostgreSQL
   (Neon): psql "<адрес базы>" -f <файл>.sql. Фото Sarpo в базе — адреса файлов в хранилище Vercel Blob.

НОВЫЙ КОМПЬЮТЕР ИЛИ ПЕРЕУСТАНОВКА
1. Хаб → Настройки → Компьютеры → «Добавить компьютер» → «Скачать установщик» и запустить его (Windows:
   «Подробнее» → «Выполнить в любом случае»).
2. Программа сама найдёт на дисках Программа/Настройки.json и возьмёт оттуда диски и язык. Файлы, что уже лежат
   здесь, остаются: программа докачает только новое и пропущенные дни.
`
	if runtime.GOOS == "windows" {
		t = strings.ReplaceAll(t, "\n", "\r\n")
	}
	return append([]byte("\xef\xbb\xbf"), t...)
}
