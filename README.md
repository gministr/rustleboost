# RustleBoost

**RustleBoost** is a free, open source Windows VPN client with TUN mode support, modern UI, and seamless integration with Remnawave-based subscription panels.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-blue.svg)
![Release](https://img.shields.io/github/v/release/gministr/rustleboost)

## Download

**[Download RustleBoost for Windows →](https://github.com/gministr/rustleboost/releases/latest)**

Website: [app.lindavpn.com](https://app.lindavpn.com)

Compatible with subscription keys from [@fastvpnboot_bot](https://t.me/fastvpnboot_bot)

## Features

- **TUN mode** — creates a virtual `RustleBoost` network interface routing all system traffic through VPN (games, system apps included)
- **Protocols** — VLESS+Reality, Hysteria2, Trojan, NaiveProxy
- **Routing modes** — All traffic / RU sites bypass / CN sites bypass
- **HWID binding** — automatic device registration for Remnawave panels
- **Real-time ping** — server latency displayed per server
- **Auto-update** — subscription list refreshes automatically
- **System tray** — runs minimized, open/close from tray
- **Light & dark theme** — toggleable from the title bar
- **Auto-update** — in-app update notifications with one-click install

## Installation

1. Download `RustleBoost_x.x.x_x64-setup.exe` from [Releases](https://github.com/gministr/rustleboost/releases)
2. Run the installer (requires administrator privileges for TUN mode)
3. Enter your subscription URL from [@fastvpnboot_bot](https://t.me/fastvpnboot_bot)
4. Select a server and click Connect

> **Note:** Windows SmartScreen may show a warning on first launch. Click "More info" → "Run anyway". This is expected for new open source applications without an EV certificate.

## Requirements

- Windows 10 or Windows 11 (x64)
- Administrator privileges (required for WinTun network interface)
- Active VPN subscription from [@fastvpnboot_bot](https://t.me/fastvpnboot_bot)

## Architecture

RustleBoost uses a three-component architecture:

| Component | Technology | Role |
|-----------|-----------|------|
| GUI | Tauri 2 + React + TypeScript | Desktop window and user interface |
| Daemon | Go | HTTP API server, subscription management, connection logic |
| Tunnel | sing-box 1.13 | VPN protocol handling (VLESS, Hysteria2, Trojan) |

## Building from Source

See [BUILD.md](BUILD.md) for full build instructions.

**Prerequisites:**
- Rust 1.77+
- Go 1.22+
- Node.js 20+ with pnpm
- Windows 10/11

```bash
git clone https://github.com/gministr/rustleboost.git
cd rustleboost
cd daemon && go build -o daemon.exe . && cd ..
pnpm install
pnpm tauri build
```

## Privacy

RustleBoost does not collect any personal data. See [PRIVACY.md](PRIVACY.md) for details.

## Code Signing

See [CODE_SIGNING.md](CODE_SIGNING.md) for our code signing policy.

## Security

To report a security vulnerability, see [SECURITY.md](SECURITY.md).

## License

MIT License — see [LICENSE](LICENSE) for details.

## Community

- Telegram channel: [@rustlevpnnews](https://t.me/rustlevpnnews)
- Support: [@rustlevpn_support](https://t.me/rustlevpn_support)
