package wg

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// Извлечение имени узла из начала соединения.
//
// Без этого правила по доменам не работают вовсе: мост tun2socks видит
// IP-пакеты и передаёт в SOCKS5 адрес, а не имя. Xray решает ту же задачу
// «сниффингом» — разбирает начало потока и достаёт имя из TLS или HTTP.
// Здесь делается то же самое, чтобы сборки правил вели себя одинаково для
// всех протоколов приложения.
//
// QUIC не разбирается: в нём ClientHello зашифрован, и достать имя без
// расшифровки начального пакета нельзя. Для QUIC остаётся сопоставление по
// адресу.

// Сколько байт читать в поисках имени.
//
// ClientHello с длинным списком расширений в этот предел укладывается, а
// ждать больше нельзя: прочитанное придётся держать в памяти и переслать
// дальше, и на каждое соединение это буфер.
const sniffLimit = 2048

// sniffHost достаёт имя узла из начала потока.
//
// Возвращает пустую строку, если имени нет: это нормально для произвольного
// TCP, и тогда правило сверяется по адресу.
func sniffHost(head []byte) string {
	if host := sniffTLSServerName(head); host != "" {
		return host
	}
	return sniffHTTPHost(head)
}

// MARK: - TLS

// sniffTLSServerName разбирает ClientHello и достаёт SNI.
func sniffTLSServerName(data []byte) string {
	// Запись TLS: тип(1) версия(2) длина(2), дальше рукопожатие.
	if len(data) < 5 || data[0] != 0x16 {
		return ""
	}
	recordLength := int(binary.BigEndian.Uint16(data[3:5]))
	body := data[5:]
	if len(body) > recordLength {
		body = body[:recordLength]
	}

	// Рукопожатие: тип(1) длина(3). Нас интересует только ClientHello.
	if len(body) < 4 || body[0] != 0x01 {
		return ""
	}
	hello := body[4:]

	// версия(2) + случайные байты(32)
	if len(hello) < 34 {
		return ""
	}
	cursor := 34

	// Идентификатор сессии.
	if len(hello) < cursor+1 {
		return ""
	}
	cursor += 1 + int(hello[cursor])

	// Наборы шифров.
	if len(hello) < cursor+2 {
		return ""
	}
	cursor += 2 + int(binary.BigEndian.Uint16(hello[cursor:cursor+2]))

	// Методы сжатия.
	if len(hello) < cursor+1 {
		return ""
	}
	cursor += 1 + int(hello[cursor])

	// Расширения.
	if len(hello) < cursor+2 {
		return ""
	}
	extensionsLength := int(binary.BigEndian.Uint16(hello[cursor : cursor+2]))
	cursor += 2
	if len(hello) < cursor+extensionsLength {
		extensionsLength = len(hello) - cursor
	}
	extensions := hello[cursor : cursor+extensionsLength]

	for len(extensions) >= 4 {
		kind := binary.BigEndian.Uint16(extensions[0:2])
		length := int(binary.BigEndian.Uint16(extensions[2:4]))
		extensions = extensions[4:]
		if len(extensions) < length {
			return ""
		}
		if kind == 0x0000 {
			return parseServerNameExtension(extensions[:length])
		}
		extensions = extensions[length:]
	}
	return ""
}

// parseServerNameExtension разбирает список имён и берёт первое имя узла.
func parseServerNameExtension(data []byte) string {
	// Длина списка(2), затем записи: тип(1) длина(2) имя.
	if len(data) < 2 {
		return ""
	}
	list := data[2:]
	for len(list) >= 3 {
		kind := list[0]
		length := int(binary.BigEndian.Uint16(list[1:3]))
		list = list[3:]
		if len(list) < length {
			return ""
		}
		// Тип 0 — имя узла; других в обиходе нет.
		if kind == 0x00 {
			return strings.ToLower(string(list[:length]))
		}
		list = list[length:]
	}
	return ""
}

// MARK: - HTTP

// sniffHTTPHost достаёт заголовок Host из начала запроса.
//
// Только открытый HTTP: по нему ходят проверки связи и часть старых
// приложений, и терять их из правил незачем.
func sniffHTTPHost(data []byte) string {
	end := bytes.Index(data, []byte("\r\n\r\n"))
	if end < 0 {
		end = len(data)
	}
	head := data[:end]

	// Первая строка должна быть запросом: иначе это не HTTP, и разбирать
	// произвольные байты как заголовки незачем.
	firstLine := bytes.IndexByte(head, '\n')
	if firstLine < 0 || !looksLikeHTTPRequest(head[:firstLine]) {
		return ""
	}

	for _, line := range bytes.Split(head[firstLine+1:], []byte("\n")) {
		name, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(string(name)), "host") {
			continue
		}
		host := strings.TrimSpace(string(value))
		// Порт в правило не входит: сверяется имя, а не адрес с портом.
		if index := strings.LastIndex(host, ":"); index > 0 && !strings.Contains(host[index:], "]") {
			host = host[:index]
		}
		return strings.ToLower(host)
	}
	return ""
}

var httpMethods = [][]byte{
	[]byte("GET "), []byte("POST "), []byte("PUT "), []byte("HEAD "),
	[]byte("DELETE "), []byte("OPTIONS "), []byte("PATCH "), []byte("CONNECT "),
	[]byte("TRACE "),
}

func looksLikeHTTPRequest(line []byte) bool {
	for _, method := range httpMethods {
		if bytes.HasPrefix(line, method) {
			return true
		}
	}
	return false
}
