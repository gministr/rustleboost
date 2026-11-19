package rbreg

import (
	"testing"
	"time"
)

func TestPacketRoundTrip(t *testing.T) {
	var key [32]byte
	key[0], key[31] = 7, 9
	pub, tok, ok := Parse(Packet(key, "abc"))
	if !ok || pub != key || tok != "abc" {
		t.Fatalf("got %v %q %v", pub, tok, ok)
	}
	if _, _, ok := Parse([]byte{0xff, 'G', 'R', 'P', 1, 2}); ok {
		t.Fatal("group hello принят за регистрацию")
	}
}

func TestToken(t *testing.T) {
	secret := []byte("s")
	now := time.Unix(1_800_000_000, 0)
	tok := MakeToken(secret, "user", now.Add(time.Hour))
	if sub, err := VerifyToken(secret, tok, now); err != nil || sub != "user" {
		t.Fatalf("%q %v", sub, err)
	}
	if _, err := VerifyToken(secret, tok, now.Add(2*time.Hour)); err == nil {
		t.Fatal("просроченный токен принят")
	}
	if _, err := VerifyToken([]byte("other"), tok, now); err == nil {
		t.Fatal("чужой секрет принят")
	}
}

func TestTunnelIPStaysInSubnet(t *testing.T) {
	for i := 0; i < 5000; i++ {
		var key [32]byte
		key[0], key[1] = byte(i), byte(i>>8)
		ip := TunnelIP(key)
		b := ip.As4()
		if b[0] != 10 || b[1] < 80 || b[1] > 95 || ip == ServerIP || b[3] == 255 || ip == subnetBase {
			t.Fatalf("адрес вне подсети: %s", ip)
		}
	}
}
