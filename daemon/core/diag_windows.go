//go:build windows

package core

// Самодиагностика подключения: после подъёма туннеля служба минуту пишет в
// журнал, что на самом деле происходит с адаптером. Ничего не меняет —
// только смотрит. Каждая системная команда — со своим сроком: зависшая
// команда не должна останавливать остальные проверки.

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/vpnclient/daemon/config"
)

func startConnectionDiag(session uint64, stale func() bool) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[diag] сбой диагностики: %v", r)
			}
		}()
		adapterIP := tunAdapterIPv4()
		log.Printf("[diag] сеанс %d: адрес адаптера %s", session, adapterIP)
		logCounters(0)
		logRoutes()

		// 1. Пинг через адаптер: на него ядро отвечает само, без сервера.
		//    Не дошёл — Windows не принимает пакеты из адаптера.
		if adapterIP.IsValid() {
			logCommand("пинг через адаптер (отвечает само ядро)", 8*time.Second, "ping", "-n", "2", "-w", "1500", "-S", adapterIP.String(), "1.1.1.1")
		}
		before := ipStats()
		logCounters(1)

		// 2. TCP и HTTP через адаптер, как у обычной программы, и DNS Windows.
		if adapterIP.IsValid() && !stale() {
			diagViaAdapter(adapterIP, "1.1.1.1:443", "http://www.gstatic.com/generate_204")
		}
		if !stale() {
			diagDNS("www.google.com")
		}
		logCounters(2)

		// 3. Сколько входящих пакетов Windows отбросила и почему.
		if !stale() {
			logStatsDiff(before, ipStats())
		}

		// 4. Брандмауэр и параметры адаптера — в конце, они медленные.
		if !stale() {
			logCommand("брандмауэр и адаптер", 15*time.Second, "powershell", "-NoProfile", "-NonInteractive", "-Command",
				"[Console]::OutputEncoding=[Text.Encoding]::UTF8; "+
					"Get-NetFirewallProfile | Format-Table Name,Enabled,DefaultInboundAction,DefaultOutboundAction,AllowInboundRules,AllowLocalFirewallRules -AutoSize | Out-String -Width 200; "+
					"Get-NetIPInterface -InterfaceAlias '"+config.TunAdapterName+"' -ErrorAction SilentlyContinue | Format-Table AddressFamily,ConnectionState,NlMtu,InterfaceMetric,Forwarding,WeakHostReceive -AutoSize | Out-String -Width 200; "+
					"Get-NetIPAddress -InterfaceAlias '"+config.TunAdapterName+"' -ErrorAction SilentlyContinue | Format-Table IPAddress,PrefixLength,AddressState -AutoSize | Out-String -Width 200; "+
					"Get-NetConnectionProfile -ErrorAction SilentlyContinue | Format-Table InterfaceAlias,NetworkCategory,IPv4Connectivity -AutoSize | Out-String -Width 200")
		}
		for i := 3; i <= 8 && !stale(); i++ {
			time.Sleep(5 * time.Second)
			logCounters(i)
		}
	}()
}

func logCounters(step int) {
	if c, ok := adapterCounters(); ok {
		log.Printf("[diag] шаг %d, адаптер: от программ %d Б, программам %d Б", step, c.Upload, c.Download)
	}
}

// tunAdapterIPv4 — адрес IPv4 адаптера RustleBoost.
func tunAdapterIPv4() netip.Addr {
	iface, err := net.InterfaceByName(config.TunAdapterName)
	if err != nil {
		return netip.Addr{}
	}
	addrs, _ := iface.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if v4 := ipn.IP.To4(); v4 != nil {
				if ip, ok := netip.AddrFromSlice(v4); ok {
					return ip
				}
			}
		}
	}
	return netip.Addr{}
}

func diagDNS(host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	started := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		log.Printf("[diag] DNS %s: ошибка за %d мс: %v", host, time.Since(started).Milliseconds(), err)
		return
	}
	if len(addrs) > 3 {
		addrs = addrs[:3]
	}
	log.Printf("[diag] DNS %s: %s за %d мс", host, strings.Join(addrs, ", "), time.Since(started).Milliseconds())
}

// diagViaAdapter — соединение, привязанное к адресу адаптера: Windows ведёт
// его через адаптер, как трафик обычной программы. На пути Xray TCP
// принимает сам стек ядра, ещё до сервера, поэтому неудача здесь означает
// поломку между Windows и адаптером, а не сервер.
func diagViaAdapter(local netip.Addr, tcpTarget, httpURL string) {
	dialer := &net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: local.AsSlice()}}
	started := time.Now()
	conn, err := dialer.Dial("tcp4", tcpTarget)
	if err != nil {
		log.Printf("[diag] TCP через адаптер к %s: ошибка за %d мс: %v", tcpTarget, time.Since(started).Milliseconds(), err)
	} else {
		log.Printf("[diag] TCP через адаптер к %s: соединение за %d мс", tcpTarget, time.Since(started).Milliseconds())
		_ = conn.Close()
	}
	client := &http.Client{Timeout: 7 * time.Second, Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}}
	started = time.Now()
	resp, err := client.Get(httpURL)
	if err != nil {
		log.Printf("[diag] HTTP через адаптер %s: ошибка за %d мс: %v", httpURL, time.Since(started).Milliseconds(), err)
		return
	}
	_ = resp.Body.Close()
	log.Printf("[diag] HTTP через адаптер %s: %d за %d мс", httpURL, resp.StatusCode, time.Since(started).Milliseconds())
}

// logRoutes — маршруты по умолчанию и половинки туннеля с метриками.
func logRoutes() {
	table, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		log.Printf("[diag] маршруты: %v", err)
		return
	}
	names := map[uint32]string{}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, i := range ifaces {
			names[uint32(i.Index)] = i.Name
		}
	}
	for i := range table {
		row := &table[i]
		prefix := row.DestinationPrefix.Prefix()
		if prefix.Bits() > 1 {
			continue
		}
		ifMetric := uint32(0)
		if ipif, err := row.InterfaceLUID.IPInterface(windows.AF_INET); err == nil {
			ifMetric = ipif.Metric
		}
		log.Printf("[diag] маршрут %s через %s (%s) метрика %d+%d", prefix, row.NextHop.Addr(), names[row.InterfaceIndex], row.Metric, ifMetric)
	}
}

// runCommand — системная команда без окна, со сроком.
func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("не ответило за %s", timeout)
	}
	if name == "powershell" {
		return strings.TrimSpace(string(out)), err
	}
	return strings.TrimSpace(decodeOEM(out)), err
}

func logCommand(title string, timeout time.Duration, name string, args ...string) {
	text, err := runCommand(timeout, name, args...)
	if err != nil {
		text += fmt.Sprintf(" (%v)", err)
	}
	log.Printf("[diag] %s:\n%s", title, text)
}

var number = regexp.MustCompile(`\d+`)

// ipStats — счётчики IP, TCP и UDP самой Windows (netsh ... show ipstats).
func ipStats() []string {
	var lines []string
	for _, what := range []string{"ipstats", "tcpstats", "udpstats"} {
		text, err := runCommand(8*time.Second, "netsh", "interface", "ipv4", "show", what)
		if err != nil {
			log.Printf("[diag] netsh %s: %v", what, err)
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, what+": "+line)
			}
		}
	}
	return lines
}

// logStatsDiff пишет строки, где счётчик Windows изменился, с прежним значением.
func logStatsDiff(before, after []string) {
	if len(before) == 0 || len(before) != len(after) {
		log.Printf("[diag] счётчики Windows: нет данных (%d/%d строк)", len(before), len(after))
		return
	}
	var changed []string
	for i := range after {
		b := number.FindAllString(before[i], -1)
		a := number.FindAllString(after[i], -1)
		if len(a) == 0 || len(a) != len(b) || a[len(a)-1] == b[len(b)-1] {
			continue
		}
		changed = append(changed, fmt.Sprintf("%s (было %s)", after[i], b[len(b)-1]))
	}
	log.Printf("[diag] счётчики Windows изменились:\n%s", strings.Join(changed, "\n"))
}

// decodeOEM — вывод консольных команд (CP866 у русской Windows) в UTF-8.
func decodeOEM(data []byte) string {
	// Новые сборки Windows пишут в консоль уже UTF-8.
	if utf8.Valid(data) {
		return string(data)
	}
	out := make([]rune, 0, len(data))
	for _, b := range data {
		switch {
		case b < 0x80:
			out = append(out, rune(b))
		case b <= 0xAF:
			out = append(out, rune(0x0410+int(b)-0x80))
		case b >= 0xE0 && b <= 0xEF:
			out = append(out, rune(0x0440+int(b)-0xE0))
		case b == 0xF0:
			out = append(out, 'Ё')
		case b == 0xF1:
			out = append(out, 'ё')
		default:
			out = append(out, ' ')
		}
	}
	return string(out)
}
