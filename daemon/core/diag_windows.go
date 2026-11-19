//go:build windows

package core

// Самодиагностика подключения: после подъёма туннеля служба минуту пишет в
// журнал, что на самом деле происходит с адаптером. Ничего не меняет —
// только смотрит: счётчики адаптера (уходят ли пакеты программ и приходят ли
// ответы), запрос через сам адаптер (как его сделала бы программа),
// разрешение имени системным DNS и маршруты. По этому журналу видно, на
// каком шаге рвётся трафик, без повторных проверок.

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

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
		logRoutes()
		adapterIP := tunAdapterIPv4()
		log.Printf("[diag] сеанс %d: адрес адаптера %s", session, adapterIP)
		for i := 0; i <= 12; i++ {
			if stale() {
				return
			}
			if c, ok := adapterCounters(); ok {
				log.Printf("[diag] +%2ds адаптер: от программ %d Б, программам %d Б", i*5, c.Upload, c.Download)
			}
			switch i {
			case 1:
				diagDNS("www.youtube.com")
				diagDNS("www.google.com")
			case 2, 6:
				if adapterIP.IsValid() {
					diagViaAdapter(adapterIP, "1.1.1.1:443", "http://www.gstatic.com/generate_204")
				}
			}
			time.Sleep(5 * time.Second)
		}
	}()
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
			if ip, ok := netip.AddrFromSlice(ipn.IP.To4()); ok && ip.Is4() {
				return ip
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
// его через адаптер, как трафик обычной программы.
func diagViaAdapter(local netip.Addr, tcpTarget, httpURL string) {
	dialer := &net.Dialer{Timeout: 6 * time.Second, LocalAddr: &net.TCPAddr{IP: local.AsSlice()}}
	started := time.Now()
	conn, err := dialer.Dial("tcp4", tcpTarget)
	if err != nil {
		log.Printf("[diag] TCP через адаптер к %s: ошибка за %d мс: %v", tcpTarget, time.Since(started).Milliseconds(), err)
	} else {
		log.Printf("[diag] TCP через адаптер к %s: соединение за %d мс", tcpTarget, time.Since(started).Milliseconds())
		_ = conn.Close()
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}}
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
		if bits := prefix.Bits(); bits > 1 {
			continue
		}
		ifMetric := uint32(0)
		connected := false
		if ipif, err := row.InterfaceLUID.IPInterface(windows.AF_INET); err == nil {
			ifMetric = ipif.Metric
			connected = ipif.Connected
		}
		log.Printf("[diag] маршрут %s через %s (%s, №%d) метрика %d+%d, подключён=%v",
			prefix, row.NextHop.Addr(), names[row.InterfaceIndex], row.InterfaceIndex, row.Metric, ifMetric, connected)
	}
	if iface, err := net.InterfaceByName(config.TunAdapterName); err == nil {
		luid, _ := winipcfg.LUIDFromIndex(uint32(iface.Index))
		if dns, err := luid.DNS(); err == nil {
			log.Printf("[diag] DNS адаптера %s: %v", config.TunAdapterName, dns)
		}
	} else {
		log.Printf("[diag] адаптер %s не найден: %v", config.TunAdapterName, err)
	}
	log.Printf("[diag] %s", fmt.Sprint("системные DNS: ", SystemDNSServers()))
}
