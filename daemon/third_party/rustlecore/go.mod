module rustleboost/rustlecore

go 1.27.1

require (
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e // indirect
	github.com/bdandy/go-errors v1.2.2 // indirect
	github.com/bdandy/go-socks4 v1.2.3 // indirect
	github.com/bogdanfinn/fhttp v0.6.8 // indirect
	github.com/bogdanfinn/quic-go-utls v1.0.9-utls // indirect
	github.com/bogdanfinn/tls-client v1.14.0 // indirect
	github.com/bogdanfinn/utls v1.7.7-barnius // indirect
	github.com/bogdanfinn/websocket v1.5.5-barnius // indirect
	github.com/cbeuw/connutil v1.0.1 // indirect
	github.com/cloudflare/circl v1.6.4 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/ghodss/yaml v1.0.1-0.20220118164431-d8423dcdf344 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pion/dtls/v3 v3.1.4 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/rtp v1.10.1 // indirect
	github.com/pion/srtp/v3 v3.0.10 // indirect
	github.com/pion/stun/v3 v3.1.6 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/pion/turn/v5 v5.0.2 // indirect
	github.com/pires/go-proxyproto v0.15.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.3-0.20260301010127-aa6edf4b11af // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/sagernet/sing v0.5.1 // indirect
	github.com/sagernet/sing-shadowsocks v0.2.7 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/tam7t/hpkp v0.0.0-20160821193359-2b70b4024ed5 // indirect
	github.com/vishvananda/netlink v1.3.1 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/xtls/reality v0.0.0-20260322125925-9234c772ba8f // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/mobile v0.0.0-20260821190718-4776eadac327 // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard/windows v1.0.1 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/grpc v1.82.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gvisor.dev/gvisor v0.0.0-20260122175437-89a5d21be8f0 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
)

require (
	github.com/apernet/hysteria/core/v2 v2.12.2
	github.com/apernet/hysteria/extras/v2 v2.12.2
	github.com/cacggghp/vk-turn-proxy v0.0.0
	github.com/xtls/xray-core v1.260327.0
	golang.org/x/crypto v0.55.0
	golang.org/x/sys v0.47.0
	golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446
	rustleboost/vkturn v0.0.0
)

// Ядро Xray берётся исходниками по закреплённому тегу: в go.mod тега v26.7.28
// не указан мажор, и прокси модулей Go его отвергает. Клонирует Scripts/fetch-cores.sh.
replace github.com/xtls/xray-core => ./.deps/xray-core

// Клиент WDTT лежит отдельным модулем рядом, но линкуется в тот же фреймворк:
// два рантайма Go в одном процессе не уживаются.
replace rustleboost/vkturn => ../vkturn

// Пакет netstack когда-то был отдельным модулем, а теперь входит в основной.
// Обе версии видны прокси модулей, и без этого импорт становится неоднозначным.
exclude golang.zx2c4.com/wireguard/tun/netstack v0.0.0-20220703234212-c31a7b1ab478

tool golang.org/x/mobile/cmd/gobind

replace github.com/cacggghp/vk-turn-proxy => ../vkturnproxy
