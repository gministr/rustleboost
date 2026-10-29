// Package rbreg — регистрация устройства на узле SRTP (RustleBoost).
//
// Клиент шлёт на каждом соединении служебный пакет: открытый ключ WireGuard
// устройства и токен подписки. Узел проверяет токен своим секретом, без
// обращения к панели, и заводит устройство в свой WireGuard. Адрес устройства
// в туннеле вычисляется из ключа, одинаково на обеих сторонах, — договариваться
// о нём не нужно.
//
// Токен выдаёт каталог: shortUuid подписки, срок и подпись HMAC. Каталог
// перечитывается приложением каждый час, срок — неделя, так что отключённая
// подписка перестаёт регистрировать новые устройства самое позднее через
// неделю, а сам токен не живёт вечно.
package rbreg

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Magic — признак пакета регистрации. Как и group hello автора, начинается с
// 0xff: такой первый байт не бывает у пакетов WireGuard (типы 1–4).
var Magic = []byte{0xff, 'R', 'B', 'R'}

// MaxTokenLen — предел длины токена; длиннее — не пакет регистрации.
const MaxTokenLen = 256

// Packet собирает пакет регистрации.
func Packet(publicKey [32]byte, token string) []byte {
	out := make([]byte, 0, len(Magic)+32+len(token))
	out = append(out, Magic...)
	out = append(out, publicKey[:]...)
	return append(out, token...)
}

// Parse разбирает пакет регистрации.
func Parse(buf []byte) (publicKey [32]byte, token string, ok bool) {
	if len(buf) < len(Magic)+32+1 || len(buf) > len(Magic)+32+MaxTokenLen || !bytes.HasPrefix(buf, Magic) {
		return publicKey, "", false
	}
	copy(publicKey[:], buf[len(Magic):len(Magic)+32])
	return publicKey, string(buf[len(Magic)+32:]), true
}

// Подсеть устройств: 10.80.0.0/12, около миллиона адресов — случайные
// совпадения при сотнях устройств практически исключены. 10.80.0.1 — узел.
var (
	subnetBase = netip.MustParseAddr("10.80.0.0")
	ServerIP   = netip.MustParseAddr("10.80.0.1")
)

// SubnetPrefix — подсеть устройств целиком, для NAT и интерфейса узла.
const SubnetPrefix = "10.80.0.0/12"

// TunnelIP — адрес устройства в туннеле, вычисленный из его ключа.
func TunnelIP(publicKey [32]byte) netip.Addr {
	sum := sha256.Sum256(publicKey[:])
	n := binary.BigEndian.Uint32(sum[:4]) % (1 << 20)
	if n < 2 {
		n += 2 // .0 — сеть, .1 — узел
	}
	if n&0xff == 0xff {
		n-- // без .255, чтобы не путаться с широковещательными
	}
	base := subnetBase.As4()
	v := binary.BigEndian.Uint32(base[:]) + n
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}

// MakeToken подписывает токен подписки.
func MakeToken(secret []byte, subscription string, expires time.Time) string {
	body := subscription + "|" + strconv.FormatInt(expires.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(body + "|" + sign(secret, body)))
}

// VerifyToken проверяет подпись и срок; возвращает подписку.
func VerifyToken(secret []byte, token string, now time.Time) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("rbreg: токен не в base64")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", errors.New("rbreg: токен не того вида")
	}
	body := parts[0] + "|" + parts[1]
	if !hmac.Equal([]byte(sign(secret, body)), []byte(parts[2])) {
		return "", errors.New("rbreg: подпись не сходится")
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > exp {
		return "", errors.New("rbreg: срок токена истёк")
	}
	return parts[0], nil
}

func sign(secret []byte, body string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}
