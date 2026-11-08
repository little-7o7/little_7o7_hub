// Package tray — значок Little Hub: у часов на Windows, в строке меню на Mac. Меню: открыть окно, сохранить сейчас,
// выход. На Linux значка нет (программа там только для разработки).
package tray

// Menu — что делают пункты меню.
type Menu struct {
	Open, Save, Quit func()
	// Tooltip — подсказка значка (обновляется раз в минуту).
	Tooltip func() string
	// Lang — язык меню (ru/en).
	Lang func() string
}

var words = map[string][4]string{
	"ru": {"Открыть Little Hub", "Сохранить сейчас", "Выход", "Little Hub"},
	"en": {"Open Little Hub", "Save now", "Quit", "Little Hub"},
}

func label(m Menu) [4]string {
	if m.Lang != nil {
		if w, ok := words[m.Lang()]; ok {
			return w
		}
	}
	return words["ru"]
}
