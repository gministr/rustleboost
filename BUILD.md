# RustleBoost — Инструкция по сборке

## Архитектура

Клиент состоит из четырёх частей:

| Слой | Технология | Роль |
|---|---|---|
| UI | React + TypeScript (Vite) | Интерфейс |
| Оболочка | Tauri 2 (Rust) | Окно, трей, автозапуск, автообновление |
| Демон | Go | REST API на localhost, подписка, управление ядрами |
| Ядра | sing-box + Xray-core | Собственно туннель |

### Почему два ядра

**sing-box** всегда держит TUN-интерфейс, DNS и маршрутизацию.

**Xray-core** несёт сам прокси. Он нужен потому, что sing-box не реализует
транспорт **XHTTP**, а именно на нём работают LTE-локации. Проверить можно
напрямую:

```
sing-box check -c config-с-xhttp.json
FATAL decode config: outbounds[0].transport: unknown transport type: xhttp
```

Схема соединения:

```
приложения → TUN (sing-box) → SOCKS 127.0.0.1:21080 → Xray → VLESS-сервер
```

Трафик самих ядер маршрутизируется `direct` по `process_name`, иначе прокси-плечо
затягивается обратно в TUN и туннель встаёт.

Протоколы, которых нет в Xray (Hysteria2, TUIC, NaiveProxy), обслуживает
sing-box напрямую — без промежуточного SOCKS.

---

## Требования

| Инструмент | Версия | Загрузка |
|---|---|---|
| Go | 1.21+ | https://go.dev/dl/ |
| Rust + Cargo | stable | https://rustup.rs |
| Node.js | 18+ | https://nodejs.org |
| pnpm | 10+ | `npm i -g pnpm` |

### Windows дополнительно
- Visual Studio Build Tools 2022 (C++ workload)
- WebView2 Runtime (предустановлен на Windows 11)

---

## 1. Бинарники ядер

Оба ядра лежат в репозитории и обновляются вручную:

```
src-tauri\binaries\sing-box-x86_64-pc-windows-msvc.exe
src-tauri\binaries\xray-x86_64-pc-windows-msvc.exe
```

Обновление:
- sing-box — https://github.com/SagerNet/sing-box/releases (`sing-box-windows-amd64.zip`)
- Xray — https://github.com/XTLS/Xray-core/releases (`Xray-windows-64.zip`, нужен только `xray.exe`;
  `geoip.dat`/`geosite.dat` не требуются — гео-правила живут в sing-box)

---

## 2. Сборка Go-демона

```powershell
cd daemon
go mod tidy
go test ./...

$env:GOOS="windows"; $env:GOARCH="amd64"
# -H windowsgui: без него Windows даёт демону консоль, и она мелькает
# чёрным окном при каждом запуске приложения. Логи идут в
# %APPDATA%\RustleBoost\daemon.log
go build -ldflags "-s -w -H windowsgui" -o ..\src-tauri\binaries\daemon-x86_64-pc-windows-msvc.exe .
cd ..
```

---

## 3. Установка JS-зависимостей

```powershell
pnpm install
```

---

## 4. Режим разработки

```powershell
pnpm tauri dev
```

---

## 5. Production сборка

```powershell
pnpm tauri build
```

Установщик: `src-tauri\target\release\bundle\nsis\RustleBoost_<версия>_x64-setup.exe`

---

## 6. Структура проекта

```
rustleboost/
├── daemon/                      # Go-демон
│   ├── api/server.go            # REST API на localhost:7777
│   ├── core/
│   │   ├── manager.go           # Состояние подключения, оркестрация ядер
│   │   ├── proc.go              # Общий супервизор процессов + очистка «осиротевших» ядер
│   │   ├── singbox.go           # sing-box + счётчик трафика через Clash API
│   │   └── xray.go              # Xray-core
│   ├── subscription/
│   │   ├── parser.go            # Загрузка подписки, заголовки, форматы
│   │   └── xray.go              # URI → Xray outbound, чтение /v2ray-json
│   ├── config/generator.go      # Генерация конфигов обоих ядер
│   └── storage/storage.go       # Настройки + кэш подписки
│
├── src/                         # React UI
│   ├── api/daemon.ts            # Типы + вызовы Tauri-команд
│   ├── store/vpnStore.ts        # Zustand
│   ├── components/
│   │   ├── SubscriptionCard.tsx # Трафик и срок подписки
│   │   ├── ServerCard.tsx       # Карточка сервера + бейдж транспорта
│   │   └── UpdateBanner.tsx     # Автообновление приложения
│   └── pages/
│
└── src-tauri/                   # Tauri (Rust)
    ├── src/commands.rs          # Tauri-команды → API демона
    └── tauri.conf.json
```

---

## 7. Поддерживаемые форматы подписки

Порядок предпочтения при обновлении:

1. **`<подписка>/v2ray-json`** — Remnawave отдаёт готовые Xray-конфиги на каждый узел.
   Используется как основной источник: панель уже проставила все нюансы транспорта,
   поэтому ничего не теряется при собственной конвертации из URI.
2. **Base64-список URI** — стандартный формат, fallback для любых панелей.
3. **JSON-конфиг** (массив Xray-конфигов или один sing-box-конфиг).

### Поддерживаемые URI-схемы

```
vless://   — tcp / ws / grpc / xhttp / httpupgrade / kcp / h2, reality / tls
vmess://   — base64-JSON
trojan://
ss://
hysteria2:// и hy2://   (через sing-box)
tuic://                 (через sing-box)
naive+https://          (через sing-box)
```

---

## 8. API демона

| Метод | Путь | Описание |
|---|---|---|
| GET | `/api/status` | Статус, трафик сессии, активные ядра |
| POST | `/api/connect` | `{"server_id":"..."}` |
| POST | `/api/connect-fastest` | Подключиться к самому быстрому серверу |
| POST | `/api/disconnect` | Отключиться |
| GET | `/api/servers` | Список серверов |
| GET/POST | `/api/subscription` | Подписка + трафик и срок действия |
| POST | `/api/ping` / `/api/ping-all` | Замер задержки |
| GET/POST | `/api/settings` | Настройки |
| GET | `/api/hwid` | Идентификатор устройства |

---

## 9. Заголовки Remnawave

Демон отправляет `x-hwid`, `x-device-os`, `x-ver-os`, `x-device-model` и читает ответные:

| Заголовок | Что значит |
|---|---|
| `subscription-userinfo` | `upload`/`download`/`total`/`expire` — то, что показано в UI |
| `x-hwid-max-devices-reached` | Лимит устройств исчерпан |
| `x-hwid-not-supported` | Панель не приняла идентификатор устройства |
| `profile-title`, `announce` | Текст в кодировке `base64:` |

Если панель отказывается отдавать список, она возвращает узел-заглушку
`0.0.0.0:1` с названием вида «Приложение не поддерживается». Такие узлы
отбрасываются, а пользователю показывается настоящая причина.

---

## 10. Выпуск релиза

Автообновление работает через `tauri-plugin-updater` и файл `latest.json`
в GitHub Releases.

```powershell
# 1. Поднять версию в package.json, src-tauri/tauri.conf.json,
#    src-tauri/Cargo.toml и APP_VERSION в src/pages/SettingsPage.tsx
# 2. Тег
git tag v1.1.0
git push origin v1.1.0
```

Дальше `.github/workflows/release.yml` соберёт демон, установщик, подпишет его
и опубликует релиз вместе с `latest.json`.

Нужны секреты репозитория:

| Секрет | Что это |
|---|---|
| `TAURI_SIGNING_PRIVATE_KEY` | Приватный ключ minisign |
| `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` | Пароль к нему |

Если ключ утерян — сгенерировать новый (`pnpm tauri signer generate`) и заменить
`plugins.updater.pubkey` в `tauri.conf.json`. **Важно:** установленные у
пользователей копии после смены ключа обновиться не смогут — потребуется
переустановка вручную.

---

## 11. Дальнейшее масштабирование

- Профили: несколько подписок с быстрым переключением
- Split tunneling по приложениям через Windows Filtering Platform
- Kill Switch на уровне WFP (сейчас флаг есть, но не задействован)
- Сборка под arm64
