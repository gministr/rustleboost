//go:build windows

package core

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/vpnclient/daemon/third_party/warpcore"
)

// setCoreDevice передаёт ядру интерфейс прямого пути (nil — снять).
func setCoreDevice(dev tun.Device) {
	if dev == nil {
		warpcore.SetTunDevice(nil)
		return
	}
	warpcore.SetTunDevice(dev)
}

// Адаптер прямого пути на Windows: Wintun, как у официального клиента
// WireGuard. Ядро работает с ним напрямую, без sing-box.

// directAdapterGUID — постоянный идентификатор адаптера: с ним Windows
// узнаёт ту же сеть при каждом подключении и не заводит новый профиль
// «Сеть 2», «Сеть 3»…
var directAdapterGUID = &windows.GUID{
	Data1: 0x52427573, Data2: 0x746c, Data3: 0x6542,
	Data4: [8]byte{0x6f, 0x6f, 0x73, 0x74, 0x57, 0x47, 0x01, 0x00},
}

type windowsDirect struct {
	native *tun.NativeTun
	luid   winipcfg.LUID
	mtu    int
	routed bool
}

func openDirectPlatform(mtu int) (directPlatform, tun.Device, error) {
	tun.WintunTunnelType = "RustleBoost"
	dev, err := tun.CreateTUNWithRequestedGUID(directAdapterName, directAdapterGUID, mtu)
	if err != nil {
		return nil, nil, fmt.Errorf("адаптер Wintun: %w", err)
	}
	native, ok := dev.(*tun.NativeTun)
	if !ok {
		_ = dev.Close()
		return nil, nil, errors.New("адаптер Wintun неизвестного вида")
	}
	w := &windowsDirect{native: native, luid: winipcfg.LUID(native.LUID()), mtu: mtu}
	if err := w.configure(); err != nil {
		_ = dev.Close()
		return nil, nil, err
	}
	return w, dev, nil
}

// retryNotFound повторяет настройку, пока Windows дорабатывает адаптер:
// первые сотни миллисекунд после создания он отвечает «не найден».
func retryNotFound(step func() error) error {
	var err error
	for attempt := 0; attempt < 25; attempt++ {
		if err = step(); !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// configure даёт адаптеру адрес и MTU. Маршрутов и DNS пока нет: до проверки
// канала через адаптер не идёт ничего (activate).
func (w *windowsDirect) configure() error {
	address := netip.PrefixFrom(directAddress, 32)
	if err := retryNotFound(func() error {
		return w.luid.SetIPAddressesForFamily(windows.AF_INET, []netip.Prefix{address})
	}); err != nil {
		return fmt.Errorf("адрес адаптера: %w", err)
	}
	return retryNotFound(func() error {
		ipif, err := w.luid.IPInterface(windows.AF_INET)
		if err != nil {
			return err
		}
		ipif.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		ipif.DadTransmits = 0
		ipif.ManagedAddressConfigurationSupported = false
		ipif.OtherStatefulConfigurationSupported = false
		ipif.NLMTU = uint32(w.mtu)
		// Наименьшая метрика: DNS адаптера Windows спрашивает первым.
		ipif.UseAutomaticMetric = false
		ipif.Metric = 0
		return ipif.Set()
	})
}

// directSplitRoutes — две половины адресов вместо одного маршрута по
// умолчанию: они точнее и потому главнее его, а сам маршрут по умолчанию
// физической сети остаётся на месте — по нему закрепитель находит шлюз.
var directSplitRoutes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/1"),
	netip.MustParsePrefix("128.0.0.0/1"),
}

func (w *windowsDirect) activate() error {
	routes := make([]*winipcfg.RouteData, 0, len(directSplitRoutes))
	for _, prefix := range directSplitRoutes {
		routes = append(routes, &winipcfg.RouteData{
			Destination: prefix,
			NextHop:     netip.IPv4Unspecified(),
			Metric:      0,
		})
	}
	if err := w.luid.SetRoutesForFamily(windows.AF_INET, routes); err != nil {
		return fmt.Errorf("маршруты в туннель: %w", err)
	}
	w.routed = true
	if err := w.luid.SetDNS(windows.AF_INET, directDNS, nil); err != nil {
		w.deactivate()
		return fmt.Errorf("DNS туннеля: %w", err)
	}
	return nil
}

func (w *windowsDirect) deactivate() {
	if !w.routed {
		return
	}
	_ = w.luid.FlushRoutes(windows.AF_INET)
	_ = w.luid.FlushDNS(windows.AF_INET)
	w.routed = false
}

// close снимает маршруты; сам адаптер удаляет из системы закрытие разводки.
func (w *windowsDirect) close() {
	w.deactivate()
}

func (w *windowsDirect) routes() pinRoutes {
	return &windowsRoutes{tun: w.luid}
}

// windowsRoutes — таблица маршрутов Windows для закрепителя.
type windowsRoutes struct {
	tun winipcfg.LUID
}

// gateway — маршрут по умолчанию с наименьшей полной метрикой (маршрут плюс
// интерфейс, как считает сама Windows), не через адаптер туннеля.
func (r *windowsRoutes) gateway() (pinGateway, bool) {
	table, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return pinGateway{}, false
	}
	best := pinGateway{}
	bestMetric := ^uint64(0)
	for i := range table {
		row := &table[i]
		if row.InterfaceLUID == r.tun || row.DestinationPrefix.Prefix().Bits() != 0 {
			continue
		}
		nextHop := row.NextHop.Addr()
		if !nextHop.Is4() || nextHop.IsUnspecified() {
			continue
		}
		metric := uint64(row.Metric)
		if ipif, err := row.InterfaceLUID.IPInterface(windows.AF_INET); err == nil {
			if !ipif.Connected {
				continue
			}
			metric += uint64(ipif.Metric)
		}
		if metric < bestMetric {
			bestMetric = metric
			best = pinGateway{Iface: uint64(row.InterfaceLUID), NextHop: nextHop}
		}
	}
	return best, bestMetric != ^uint64(0)
}

func (r *windowsRoutes) add(ip netip.Addr, gw pinGateway) error {
	err := winipcfg.LUID(gw.Iface).AddRoute(netip.PrefixFrom(ip, 32), gw.NextHop, 0)
	if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
		return errPinExists
	}
	return err
}

func (r *windowsRoutes) remove(ip netip.Addr, gw pinGateway) error {
	return winipcfg.LUID(gw.Iface).DeleteRoute(netip.PrefixFrom(ip, 32), gw.NextHop)
}
