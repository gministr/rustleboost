package wg

import (
	"bytes"
	"net"
	"testing"
)

// Разбор датаграмм SOCKS5 — то место, где ошибка не падает, а тихо ломает
// DNS и QUIC. Длина заголовка зависит от типа адреса, и отделение полезной
// нагрузки от него проверяется здесь явно.

func udpPacket(atyp byte, addr []byte, port uint16, payload []byte) []byte {
	packet := []byte{0x00, 0x00, 0x00, atyp}
	packet = append(packet, addr...)
	packet = append(packet, byte(port>>8), byte(port))
	return append(packet, payload...)
}

func TestParseUDPRequestIPv4(t *testing.T) {
	payload := []byte("запрос DNS")
	packet := udpPacket(addrIPv4, []byte{8, 8, 8, 8}, 53, payload)

	got, target, err := parseUDPRequest(packet)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if target != "8.8.8.8:53" {
		t.Errorf("адрес = %q, ожидался 8.8.8.8:53", target)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("нагрузка = %q, ожидалась %q", got, payload)
	}
}

func TestParseUDPRequestDomain(t *testing.T) {
	name := "example.com"
	addr := append([]byte{byte(len(name))}, name...)
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	packet := udpPacket(addrDomain, addr, 443, payload)

	got, target, err := parseUDPRequest(packet)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if target != "example.com:443" {
		t.Errorf("адрес = %q, ожидался example.com:443", target)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("нагрузка = %q, ожидалась %q", got, payload)
	}
}

func TestParseUDPRequestIPv6(t *testing.T) {
	ip := net.ParseIP("2001:4860:4860::8888").To16()
	payload := []byte("quic")
	packet := udpPacket(addrIPv6, ip, 443, payload)

	got, target, err := parseUDPRequest(packet)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if target != "[2001:4860:4860::8888]:443" {
		t.Errorf("адрес = %q", target)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("нагрузка = %q, ожидалась %q", got, payload)
	}
}

// Пустая нагрузка допустима и не должна приводить к выходу за границы среза.
func TestParseUDPRequestEmptyPayload(t *testing.T) {
	packet := udpPacket(addrIPv4, []byte{1, 1, 1, 1}, 53, nil)

	got, target, err := parseUDPRequest(packet)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if target != "1.1.1.1:53" {
		t.Errorf("адрес = %q", target)
	}
	if len(got) != 0 {
		t.Errorf("нагрузка должна быть пустой, получено %d байт", len(got))
	}
}

// Сборка фрагментов не поддерживается: полумера здесь опаснее явного отказа.
func TestParseUDPRequestRejectsFragments(t *testing.T) {
	packet := udpPacket(addrIPv4, []byte{1, 1, 1, 1}, 53, []byte("x"))
	packet[2] = 0x01

	if _, _, err := parseUDPRequest(packet); err == nil {
		t.Fatal("фрагмент должен быть отвергнут")
	}
}

func TestParseUDPRequestRejectsTruncated(t *testing.T) {
	if _, _, err := parseUDPRequest([]byte{0x00, 0x00}); err == nil {
		t.Fatal("обрезанная датаграмма должна быть отвергнута")
	}
}

func TestBuildUDPReplyRoundTrip(t *testing.T) {
	payload := []byte("ответ DNS")
	from := &net.UDPAddr{IP: net.ParseIP("8.8.4.4"), Port: 53}

	packet, err := buildUDPReply(payload, from)
	if err != nil {
		t.Fatalf("сборка не удалась: %v", err)
	}

	// Ответ должен разбираться тем же разборщиком: формат заголовка общий.
	got, target, err := parseUDPRequest(packet)
	if err != nil {
		t.Fatalf("собранный ответ не разбирается: %v", err)
	}
	if target != "8.8.4.4:53" {
		t.Errorf("адрес = %q", target)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("нагрузка = %q, ожидалась %q", got, payload)
	}
}

// Клиенту нужен реальный порт привязки, иначе он не знает, куда слать
// датаграммы, и UDP молча не работает.
func TestWriteReplyCarriesBoundPort(t *testing.T) {
	var buf bytes.Buffer
	conn := &fakeConn{Writer: &buf}

	if err := writeReply(conn, replySuccess, net.IPv4(127, 0, 0, 1), 51820); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	out := buf.Bytes()
	if len(out) != 10 {
		t.Fatalf("длина ответа %d, ожидалось 10", len(out))
	}
	if out[3] != addrIPv4 {
		t.Errorf("тип адреса = %d", out[3])
	}
	if !bytes.Equal(out[4:8], []byte{127, 0, 0, 1}) {
		t.Errorf("адрес = %v", out[4:8])
	}
	if port := int(out[8])<<8 | int(out[9]); port != 51820 {
		t.Errorf("порт = %d, ожидался 51820", port)
	}
}

type fakeConn struct {
	net.Conn
	Writer *bytes.Buffer
}

func (f *fakeConn) Write(p []byte) (int, error) { return f.Writer.Write(p) }
