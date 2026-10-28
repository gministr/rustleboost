package core

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Probe targets: two unrelated companies. A tunnel is judged dead only when
// neither answers, so a server that blocks one of them cannot make a working
// tunnel look broken.
var probeTargets = []struct{ host, path string }{
	{"www.gstatic.com", "/generate_204"},
	{"cp.cloudflare.com", "/generate_204"},
}

// livenessProbe makes a real HTTP request through a SOCKS5 proxy on
// 127.0.0.1:port. It exercises the whole proxy leg — the core, the transport,
// the node and its way out — which is the only check that catches "everything
// is running but nothing loads".
//
// Any HTTP answer counts as alive, including a redirect to a captive portal:
// the question is whether traffic flows, not whether the internet is open.
func livenessProbe(port int, total time.Duration) bool {
	deadline := time.Now().Add(total)
	for _, target := range probeTargets {
		left := time.Until(deadline)
		if left < 300*time.Millisecond {
			return false
		}
		// Half of what is left for the first target, so the second still gets
		// a turn when the first is black-holed.
		if socksHTTPGet(port, target.host, target.path, left/2+left%2) {
			return true
		}
	}
	return false
}

func socksHTTPGet(port int, host, path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	// Greeting: version 5, one method — none.
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return false
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
		return false
	}

	// CONNECT by name, so the node resolves it rather than this machine.
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	port80 := make([]byte, 2)
	binary.BigEndian.PutUint16(port80, 80)
	req = append(req, port80...)
	if _, err := conn.Write(req); err != nil {
		return false
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0 {
		return false
	}
	var skip int
	switch head[3] {
	case 1:
		skip = 4 + 2
	case 4:
		skip = 16 + 2
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return false
		}
		skip = int(l[0]) + 2
	default:
		return false
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip)); err != nil {
		return false
	}

	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\nUser-Agent: RustleBoost\r\n\r\n", path, host); err != nil {
		return false
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return err == nil && strings.HasPrefix(line, "HTTP/1.")
}
