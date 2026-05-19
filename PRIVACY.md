# Privacy Policy

**Last updated: May 2026**

## Overview

RustleBoost is an open source VPN client. We are committed to protecting your privacy.

## Data We Do NOT Collect

RustleBoost does **not** collect, store, or transmit:

- Personal identification information
- Browsing history or traffic logs
- IP addresses or connection metadata
- Crash reports or telemetry
- Usage statistics or analytics

## Data Stored Locally

The following data is stored **only on your device** in `%APPDATA%\RustleBoost\`:

| File | Contents | Purpose |
|------|----------|---------|
| `settings.json` | Subscription URL, preferences | App configuration |
| `sing-box-config.json` | Generated VPN config | Tunnel operation |
| `sing-box.log` | Connection logs | Debugging |

These files never leave your device and are not transmitted anywhere.

## Subscription URL

Your subscription URL (entered during setup) is stored locally and used only to fetch the server list from your VPN provider. RustleBoost does not have access to or control over your VPN provider's privacy practices.

## HWID

A hardware identifier (HWID) is generated from your device hardware and sent to your VPN provider's subscription endpoint for device binding. This is required by Remnawave-based panels. RustleBoost does not store or transmit this identifier anywhere else.

## Third-Party Services

RustleBoost uses **sing-box** as the underlying VPN tunnel. sing-box is open source and does not collect data.

## Contact

For privacy questions: [@rustlevpn_support](https://t.me/rustlevpn_support)
