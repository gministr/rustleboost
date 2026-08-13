package core

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/vpnclient/daemon/config"
	"github.com/vpnclient/daemon/subscription"
)

const (
	// probePortBase sits well clear of the ports the running tunnel uses.
	probePortBase = 23100
	probeTimeout  = 8 * time.Second
	// Xray needs a moment to bind every inbound before requests can land.
	probeStartupGrace = 1200 * time.Millisecond
)

// LatencyProber measures how long a real request takes through each node.
//
// It runs a throwaway Xray with one SOCKS inbound per node and issues a GET
// through all of them at once. The number that comes back therefore includes
// the handshake, the proxy hop and the return trip — the same thing the user
// feels — instead of the bare TCP round trip to the node's front door, which
// for CDN-fronted nodes is identical everywhere and tells them nothing.
type LatencyProber struct {
	mu      sync.Mutex
	dataDir string
	runner  *XrayRunner
}

func NewLatencyProber(dataDir string) *LatencyProber {
	return &LatencyProber{
		dataDir: dataDir,
		runner:  NewXrayProbeRunner(dataDir),
	}
}

// Measure returns latency in milliseconds per server ID; -1 means unreachable.
//
// Nodes carried by sing-box (Hysteria2, TUIC, Naive) fall back to a TCP
// measurement: they have no Xray outbound to route a probe through.
func (p *LatencyProber) Measure(servers []subscription.Server) map[string]int {
	results := make(map[string]int, len(servers))

	var viaXray []subscription.Server
	var viaTCP []subscription.Server
	for _, s := range servers {
		if config.NeedsXray(s) {
			viaXray = append(viaXray, s)
		} else {
			viaTCP = append(viaTCP, s)
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, s := range viaTCP {
		wg.Add(1)
		go func(srv subscription.Server) {
			defer wg.Done()
			ms := pingTCP(srv.Address, srv.Port)
			mu.Lock()
			results[srv.ID] = ms
			mu.Unlock()
		}(s)
	}

	if len(viaXray) > 0 {
		proxied, err := p.measureViaProxy(viaXray)
		if err != nil {
			log.Printf("[latency] proxy probe failed, falling back to TCP: %v", err)
			for _, s := range viaXray {
				wg.Add(1)
				go func(srv subscription.Server) {
					defer wg.Done()
					ms := pingTCP(srv.Address, srv.Port)
					mu.Lock()
					results[srv.ID] = ms
					mu.Unlock()
				}(s)
			}
		} else {
			for id, ms := range proxied {
				results[id] = ms
			}
		}
	}

	wg.Wait()
	return results
}

func (p *LatencyProber) measureViaProxy(servers []subscription.Server) (map[string]int, error) {
	// One probe at a time: a second run would collide on the same ports.
	p.mu.Lock()
	defer p.mu.Unlock()

	ports, err := reserveProbePorts(len(servers))
	if err != nil {
		return nil, err
	}

	targets := make([]config.ProbeTarget, len(servers))
	for i, s := range servers {
		targets[i] = config.ProbeTarget{ServerID: s.ID, Outbound: s.Outbound, Port: ports[i]}
	}

	cfg, err := config.GenerateXrayProbe(targets)
	if err != nil {
		return nil, err
	}
	if err := p.runner.StartProbe(cfg); err != nil {
		return nil, err
	}
	defer p.runner.Stop()

	time.Sleep(probeStartupGrace)

	results := make(map[string]int, len(servers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, s := range servers {
		wg.Add(1)
		go func(srv subscription.Server, port int) {
			defer wg.Done()
			ms := probeThroughSocks(port)
			mu.Lock()
			results[srv.ID] = ms
			mu.Unlock()
		}(s, ports[i])
	}
	wg.Wait()

	return results, nil
}

// probeThroughSocks times one request through a local SOCKS port.
func probeThroughSocks(port int) int {
	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	if err != nil {
		return -1
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			DisableKeepAlives: true,
		},
		Timeout: probeTimeout,
		// A redirect would add a second round trip and skew the number.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	resp, err := client.Get(probeURL)
	if err != nil {
		return -1
	}
	resp.Body.Close()

	return int(time.Since(start).Milliseconds())
}

// reserveProbePorts finds n free local ports starting from probePortBase.
func reserveProbePorts(n int) ([]int, error) {
	ports := make([]int, 0, n)

	for port := probePortBase; port < probePortBase+400 && len(ports) < n; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		listener.Close()
		ports = append(ports, port)
	}

	if len(ports) < n {
		return nil, fmt.Errorf("only %d of %d probe ports available", len(ports), n)
	}
	return ports, nil
}
