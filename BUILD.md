# LindaVPN — Инструкция по сборке

## Требования

| Инструмент | Версия | Загрузка |
|---|---|---|
| Go | 1.21+ | https://go.dev/dl/ |
| Rust + Cargo | stable | https://rustup.rs |
| Node.js | 18+ | https://nodejs.org |
| pnpm | 8+ | `npm i -g pnpm` |
| sing-box | latest | https://github.com/SagerNet/sing-box/releases |

### Windows дополнительно
- Visual Studio Build Tools 2022 (C++ workload)
- WebView2 Runtime (предустановлен на Windows 11)

---

## 1. Подготовка бинарников

### sing-box
Скачай `sing-box-windows-amd64.zip` с GitHub Releases и распакуй `sing-box.exe`:
```
sing-box-windows-amd64\sing-box.exe  →  src-tauri\binaries\sing-box-x86_64-pc-windows-msvc.exe
```

### Создай директорию binaries
```powershell
mkdir src-tauri\binaries
```

---

## 2. Сборка Go демона

```powershell
cd daemon

# Загрузить зависимости
go mod tidy

# Собрать для Windows x64
$env:GOOS="windows"; $env:GOARCH="amd64"
go build -o ..\src-tauri\binaries\daemon-x86_64-pc-windows-msvc.exe .

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
# В одном терминале
pnpm tauri dev
```

Приложение откроется с hot-reload для React-части.

---

## 5. Production сборка

```powershell
pnpm tauri build
```

Установщик появится в:
```
src-tauri\target\release\bundle\nsis\LindaVPN_1.0.0_x64-setup.exe
```

---

## 6. Структура проекта

```
vpn-client/
├── daemon/                  # Go-демон (proxy manager + HTTP API)
│   ├── api/server.go        # REST API на localhost:7777
│   ├── core/
│   │   ├── manager.go       # Управление подключениями
│   │   └── singbox.go       # Запуск/остановка sing-box
│   ├── subscription/
│   │   └── parser.go        # Парсер subscription URL
│   ├── config/
│   │   └── generator.go     # Генератор sing-box конфигов
│   └── storage/
│       └── storage.go       # Сохранение настроек
│
├── src/                     # React TypeScript UI
│   ├── api/daemon.ts        # Типы + вызовы Tauri commands
│   ├── store/vpnStore.ts    # Zustand хранилище
│   └── pages/
│       ├── MainPage.tsx     # Главный экран + кнопка Connect
│       ├── ServersPage.tsx  # Список серверов
│       └── SettingsPage.tsx # Настройки + подписка
│
└── src-tauri/               # Tauri (Rust) — оболочка
    ├── src/
    │   ├── main.rs          # Точка входа
    │   ├── daemon.rs        # Запуск Go sidecar
    │   ├── commands.rs      # Tauri команды → daemon API
    │   └── tray.rs          # Трей-иконка
    └── tauri.conf.json      # Конфигурация окна и bundle
```

---

## 7. Поддерживаемые форматы подписки

- **Base64-encoded URI list** — стандартный формат V2Ray/Xray
- **sing-box JSON** — нативный формат sing-box outbounds
- **Прямой список URI** — без кодирования

### Поддерживаемые URI-схемы
```
vless://uuid@host:port?security=reality&pbk=...&sid=...&sni=...&flow=xtls-rprx-vision#Name
hysteria2://password@host:port?sni=...#Name
hy2://password@host:port#Name
naive+https://user:pass@host:port#Name
trojan://password@host:port?sni=...#Name
```

---

## 8. API демона

| Метод | Путь | Описание |
|---|---|---|
| GET | `/api/status` | Статус подключения |
| POST | `/api/connect` | `{"server_id":"..."}` |
| POST | `/api/disconnect` | Отключиться |
| GET | `/api/servers` | Список серверов |
| GET/POST | `/api/subscription` | Получить/обновить подписку |
| POST | `/api/ping` | Пинг конкретного сервера |
| POST | `/api/ping-all` | Пинг всех серверов |
| GET/POST | `/api/settings` | Настройки |

---

## 9. Переменные конфигурации sing-box

Демон автоматически генерирует конфиг sing-box на основе:
- выбранного сервера и его протокола
- настроек TUN-режима, DNS-режима
- флага KillSwitch

Конфиг сохраняется в `%APPDATA%\LindaVPN\sing-box-config.json`.

---

## 10. HWID привязка

При запросе подписки заголовок `X-Device-ID` содержит SHA256 от UUID материнской платы (Windows `wmic csproduct get UUID`). Remnawave-панель использует это для привязки устройств.

---

## 11. Дальнейшее масштабирование

- **Автообновление клиента**: Tauri plugin updater + GitHub Releases
- **Статистика трафика**: sing-box экспериментальный clash API (`/traffic`)
- **Профили**: несколько подписок с быстрым переключением
- **Кастомные правила**: bypass-list для RU-ресурсов
- **WireGuard-ядро**: добавить как альтернативный outbound в sing-box
- **Split tunneling**: routing rules по приложениям через Windows Filtering Platform
