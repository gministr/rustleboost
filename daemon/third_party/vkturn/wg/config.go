package wg

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Разбор конфигурации WireGuard, присланной сервером wdtt-server.
//
// Сервер отдаёт её в формате wg-quick — том же, что кладут в .conf. Готовый
// разборщик из состава wireguard-go лежит в пакете команды wg-quick и наружу
// не выведен, а формат достаточно мал, чтобы разобрать его здесь и не тянуть
// зависимость ради двух секций.

// Config — разобранная конфигурация.
type Config struct {
	PrivateKey    string
	Addresses     []netip.Prefix
	DNS           []netip.Addr
	MTU           int
	PeerPublicKey string
	PresharedKey  string
	AllowedIPs    []netip.Prefix
	Keepalive     int
}

// MTU по умолчанию для тройной инкапсуляции WireGuard → DTLS → TURN.
//
// Без запаса пакеты фрагментируются, а часть фрагментов relay теряет, и
// соединение выглядит как «подключилось, но ничего не грузится».
const defaultMTU = 1280

// ParseQuick разбирает конфигурацию формата wg-quick.
func ParseQuick(text string) (Config, error) {
	var cfg Config
	cfg.MTU = defaultMTU

	section := ""
	for lineNumber, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return Config{}, fmt.Errorf("строка %d: ожидалось «ключ = значение», получено %q", lineNumber+1, line)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		if err := cfg.assign(section, key, value); err != nil {
			return Config{}, fmt.Errorf("строка %d: %w", lineNumber+1, err)
		}
	}

	if cfg.PrivateKey == "" {
		return Config{}, fmt.Errorf("в конфигурации нет PrivateKey")
	}
	if cfg.PeerPublicKey == "" {
		return Config{}, fmt.Errorf("в конфигурации нет PublicKey пира")
	}
	if len(cfg.Addresses) == 0 {
		return Config{}, fmt.Errorf("в конфигурации нет Address")
	}
	if len(cfg.AllowedIPs) == 0 {
		// Сервер туннелирует всё: пустой список означал бы, что в туннель не
		// уйдёт ни один пакет, и это почти наверняка не то, что имелось в виду.
		cfg.AllowedIPs = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	}
	return cfg, nil
}

func (c *Config) assign(section, key, value string) error {
	switch section {
	case "interface":
		switch key {
		case "privatekey":
			c.PrivateKey = value
		case "address":
			prefixes, err := parsePrefixList(value)
			if err != nil {
				return err
			}
			c.Addresses = append(c.Addresses, prefixes...)
		case "dns":
			for _, item := range splitList(value) {
				address, err := netip.ParseAddr(item)
				if err != nil {
					return fmt.Errorf("DNS %q: %w", item, err)
				}
				c.DNS = append(c.DNS, address)
			}
		case "mtu":
			mtu, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("MTU %q: %w", value, err)
			}
			c.MTU = mtu
		}
	case "peer":
		switch key {
		case "publickey":
			c.PeerPublicKey = value
		case "presharedkey":
			c.PresharedKey = value
		case "allowedips":
			prefixes, err := parsePrefixList(value)
			if err != nil {
				return err
			}
			c.AllowedIPs = append(c.AllowedIPs, prefixes...)
		case "persistentkeepalive":
			keepalive, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("PersistentKeepalive %q: %w", value, err)
			}
			c.Keepalive = keepalive
		}
		// Endpoint из конфигурации сервера намеренно игнорируется: он указывает
		// на публичный адрес VPS, а пакеты должны уходить в локальный порт
		// TURN-клиента, который подставляется при запуске.
	}
	return nil
}

// ApplyFallbackDNS подставляет адреса, если сервер своих не назвал.
//
// Конфигурации wdtt-server строку DNS содержат не всегда, а без неё стек не
// разрешает имена: датаграмма с доменом в заголовке пропадает молча.
func (c *Config) ApplyFallbackDNS(servers []string) {
	if len(c.DNS) > 0 || len(servers) == 0 {
		return
	}
	for _, item := range servers {
		if address, err := netip.ParseAddr(strings.TrimSpace(item)); err == nil {
			c.DNS = append(c.DNS, address)
		}
	}
}

func parsePrefixList(value string) ([]netip.Prefix, error) {
	items := splitList(value)
	prefixes := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		if prefix, err := netip.ParsePrefix(item); err == nil {
			prefixes = append(prefixes, prefix)
			continue
		}
		// Голый адрес без маски встречается в поле Address: считаем его
		// хостовым префиксом, как это делает wg-quick.
		address, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("адрес %q: %w", item, err)
		}
		prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
	}
	return prefixes, nil
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// uapi собирает конфигурацию в формате, который понимает device.IpcSet.
//
// Ключи там шестнадцатеричные, а не base64: UAPI — внутренний протокол
// wireguard-go, и текстовое представление ключей у него своё.
func (c Config) uapi(endpoint netip.AddrPort) (string, error) {
	privateKey, err := hexKey(c.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("PrivateKey: %w", err)
	}
	publicKey, err := hexKey(c.PeerPublicKey)
	if err != nil {
		return "", fmt.Errorf("PublicKey: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", privateKey)
	fmt.Fprintf(&b, "public_key=%s\n", publicKey)
	fmt.Fprintf(&b, "endpoint=%s\n", endpoint.String())

	if c.PresharedKey != "" {
		presharedKey, err := hexKey(c.PresharedKey)
		if err != nil {
			return "", fmt.Errorf("PresharedKey: %w", err)
		}
		fmt.Fprintf(&b, "preshared_key=%s\n", presharedKey)
	}
	for _, allowed := range c.AllowedIPs {
		fmt.Fprintf(&b, "allowed_ip=%s\n", allowed.String())
	}
	if c.Keepalive > 0 {
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", c.Keepalive)
	}
	return b.String(), nil
}

func hexKey(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("ключ не в base64: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("длина ключа %d байт вместо 32", len(raw))
	}
	return hex.EncodeToString(raw), nil
}
