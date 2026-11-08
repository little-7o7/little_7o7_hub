# Little Hub

Программа для компьютеров владельца little_7o7 (Windows и Mac): хранит копии хаба по дням на своих дисках и присылает
хабу курсы валют с узбекских сайтов.

## Установка

Хаб → Настройки → Компьютеры → «Добавить компьютер»: там код и команда.

- Windows — «Скачать установщик» в хабе (LittleHub-Setup.exe с адресом хаба и кодом в хвосте файла, `internal/setup`), или PowerShell: `irm https://little-7o7.vercel.app/pc/install.ps1?code=… | iex`
- Mac — Терминал: `curl -fsSL "https://little-7o7.vercel.app/pc/install.sh?code=…" | sh`

Программа ставится в папку пользователя (без прав администратора), запускается при входе, значок — у часов (Windows)
или в строке меню (Mac). Копии: `<диск>/Little Hub/Хаб/Копии базы/<месяц>/hub-<день>.l7copy`, открываются паролем
копий в окне программы → «Расшифровать копию».

Удалить: `LittleHub --uninstall` (копии на дисках остаются).

## Разработка

```sh
go test ./...
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o LittleHub.exe ./cmd/littlehub
go run tools/icons/main.go      # значки
go run tools/fakehub/main.go 127.0.0.1:47700   # поддельный хаб для ручной проверки
```
