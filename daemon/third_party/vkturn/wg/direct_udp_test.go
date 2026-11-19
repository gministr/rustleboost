package wg

import (
	"net"
	"testing"
	"time"
)

func TestResolveTarget(t *testing.T) {
	router := NewRouter([]Rule{
		{Action: ActionDirect, IPRanges: []string{"95.163.0.0/16"}, Domains: []string{"domain:ya.ru"}},
		{Action: ActionBlock, IPRanges: []string{"10.1.0.0/16"}},
	})
	cases := map[string]Action{
		"95.163.1.1:443": ActionDirect,
		"8.8.8.8:53":     ActionProxy,
		"10.1.2.3:53":    ActionBlock,
		"ya.ru:443":      ActionDirect,
		"example.com:80": ActionProxy,
	}
	for target, want := range cases {
		if got := router.ResolveTarget(target); got != want {
			t.Errorf("%s: %s, ожидалось %s", target, got, want)
		}
	}
	var empty *Router
	if empty.Active() || NewRouter(nil).Active() {
		t.Error("пустая сборка не должна считаться активной")
	}
	if !router.Active() {
		t.Error("сборка с правилами должна считаться активной")
	}
}

// Датаграмма уходит настоящим сокетом, ответ возвращается клиенту моста.
func TestDirectUDPRoundTrip(t *testing.T) {
	loopback := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	server, err := net.ListenUDP("udp", loopback)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() {
		buffer := make([]byte, 64)
		read, from, err := server.ReadFromUDP(buffer)
		if err == nil {
			_, _ = server.WriteToUDP(append([]byte("echo:"), buffer[:read]...), from)
		}
	}()

	client, err := net.ListenUDP("udp", loopback)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	local, err := net.ListenUDP("udp", loopback)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	direct := NewDirectUDP(local, func(payload []byte, from *net.UDPAddr) ([]byte, error) {
		return buildUDPReply(payload, from)
	}, nil)
	defer direct.Close()

	if err := direct.Send(client.LocalAddr().(*net.UDPAddr), []byte("ping"), server.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buffer := make([]byte, 128)
	read, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := parseUDPRequest(buffer[:read])
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "echo:ping" {
		t.Fatalf("ответ %q", payload)
	}
}
