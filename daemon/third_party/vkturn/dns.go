package vkturn

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Публичные DNS-серверы пробуются по очереди. Держим несколько независимых
// провайдеров, чтобы отказ/блокировка одного не убивала резолвинг целиком.
var publicDNSServers = []string{
	"77.88.8.8:53", // Yandex
	"77.88.8.1:53", // Yandex (второй)
	"1.1.1.1:53",   // Cloudflare
	"1.0.0.1:53",   // Cloudflare (второй)
	"8.8.8.8:53",   // Google
	"8.8.4.4:53",   // Google (второй)
}

const (
	dnsPerServerTimeout = 1500 * time.Millisecond
	dnsRoundTimeout     = 2500 * time.Millisecond
)

// Последний рубеж при жёстком глушении: если ВСЕ публичные DNS недоступны,
// отвечаем на запросы нужных хостов ВК/OK из зашитой таблицы, в обход DNS.
// IP берутся из собственных диапазонов VK/Mail.ru/OK (не сторонний CDN),
// поэтому меняются редко и годятся как аварийный резерв. Используется ТОЛЬКО
// когда обычный резолвинг полностью провалился — на рабочий путь не влияет.
var staticHostIPs = map[string][]string{
	"api.vk.me": {
		"87.240.137.130", "87.240.137.206", "87.240.137.207",
		"87.240.129.140", "87.240.190.70", "93.186.225.205",
	},
	"login.vk.ru":  {"93.186.237.1", "95.213.56.1"},
	"login.vk.com": {"93.186.237.1", "95.213.56.1"},
	"calls.okcdn.ru": {
		"155.212.204.12", "155.212.204.136", "155.212.204.195",
	},
}

func setupGlobalResolver() {
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial:     resolverDial,
	}
}

// resolverDial возвращает конец net.Pipe, обслуживаемый failover-логикой:
// каждый DNS-запрос Go-резолвера реально отправляется публичным серверам по
// очереди (UDP, затем TCP), первый валидный ответ выигрывает. Если не ответил
// НИ ОДИН сервер — отвечаем из зашитой таблицы staticHostIPs. Возвращать conn
// сразу (без ошибки) корректно: вся работа идёт при обмене DNS-сообщениями.
//
// Важно: нельзя определять живость UDP-сервера по успеху Dial — UDP
// «дозванивается» всегда (нет установления соединения). Поэтому фейловер
// строится на фактическом получении ответа, а не на успехе дозвона.
func resolverDial(ctx context.Context, network, _ string) (net.Conn, error) {
	clientSide, serverSide := net.Pipe()
	go serveFailoverDNS(serverSide)
	return clientSide, nil
}

func serveFailoverDNS(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		query, err := readDNSMessage(conn)
		if err != nil {
			return
		}

		resp := resolveViaPublicServers(query)
		if resp == nil {
			// Ни один публичный сервер не ответил — аварийная статика.
			resp, err = buildStaticDNSResponse(query)
			if err != nil {
				return
			}
		}

		if err := writeDNSMessage(conn, resp); err != nil {
			return
		}
	}
}

// resolveViaPublicServers опрашивает все серверы ПАРАЛЛЕЛЬНО (UDP и TCP
// одновременно) и возвращает первый валидный ответ. Параллельность важна: под
// глушением мёртвые серверы «висят» до таймаута, а последовательный перебор
// 6 серверов × (udp+tcp) занял бы десятки секунд и превысил бы дедлайн вызова,
// не дав включиться статике. Здесь общий предел — dnsRoundTimeout, после чего
// вызывающая сторона уходит на аварийную статику. nil — если не ответил никто.
func resolveViaPublicServers(query []byte) []byte {
	wantID, ok := dnsMessageID(query)
	if !ok {
		return nil
	}

	results := make(chan []byte, len(publicDNSServers)*2)
	for _, server := range publicDNSServers {
		go func(s string) { results <- queryDNS("udp", s, query, wantID) }(server)
		go func(s string) { results <- queryDNS("tcp", s, query, wantID) }(server)
	}

	timeout := time.After(dnsRoundTimeout)
	pending := len(publicDNSServers) * 2
	for pending > 0 {
		select {
		case resp := <-results:
			pending--
			if resp != nil {
				return resp
			}
		case <-timeout:
			return nil
		}
	}
	return nil
}

func queryDNS(network, server string, query []byte, wantID uint16) []byte {
	dialer := &net.Dialer{Timeout: dnsPerServerTimeout}
	conn, err := dialer.Dial(network, server)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(dnsPerServerTimeout))

	if network == "tcp" {
		if err := writeDNSMessage(conn, query); err != nil {
			return nil
		}
		resp, err := readDNSMessage(conn)
		if err != nil {
			return nil
		}
		if id, ok := dnsMessageID(resp); !ok || id != wantID {
			return nil
		}
		return resp
	}

	// UDP: сырое сообщение без length-префикса.
	if _, err := conn.Write(query); err != nil {
		return nil
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return nil
	}
	resp := buf[:n]
	if id, ok := dnsMessageID(resp); !ok || id != wantID {
		return nil
	}
	return resp
}

func dnsMessageID(msg []byte) (uint16, bool) {
	if len(msg) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(msg[:2]), true
}

// readDNSMessage читает length-prefixed сообщение (потоковый формат, который
// Go-резолвер использует поверх нашего net.Pipe и который применяется в DNS/TCP).
func readDNSMessage(conn net.Conn) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := readFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
	if _, err := readFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeDNSMessage(conn net.Conn, msg []byte) error {
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(msg)))
	out := make([]byte, 0, len(msg)+2)
	out = append(out, lenBuf[:]...)
	out = append(out, msg...)
	_, err := conn.Write(out)
	return err
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func buildStaticDNSResponse(query []byte) ([]byte, error) {
	var p dnsmessage.Parser
	header, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}

	respHeader := dnsmessage.Header{
		ID:            header.ID,
		Response:      true,
		Authoritative: true,
		RCode:         dnsmessage.RCodeSuccess,
	}
	builder := dnsmessage.NewBuilder(nil, respHeader)
	builder.EnableCompression()
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(q); err != nil {
		return nil, err
	}

	// Отвечаем только на A-запросы известных имён. Для AAAA/прочего —
	// пустой успешный ответ: резолвер сам возьмёт IPv4.
	name := strings.TrimSuffix(strings.ToLower(q.Name.String()), ".")
	if q.Type == dnsmessage.TypeA {
		if ips, ok := staticHostIPs[name]; ok {
			if err := builder.StartAnswers(); err != nil {
				return nil, err
			}
			for _, ipStr := range ips {
				ip := net.ParseIP(ipStr).To4()
				if ip == nil {
					continue
				}
				var a [4]byte
				copy(a[:], ip)
				rh := dnsmessage.ResourceHeader{
					Name:  q.Name,
					Type:  dnsmessage.TypeA,
					Class: dnsmessage.ClassINET,
					TTL:   60,
				}
				if err := builder.AResource(rh, dnsmessage.AResource{A: a}); err != nil {
					return nil, err
				}
			}
		}
	}

	return builder.Finish()
}
