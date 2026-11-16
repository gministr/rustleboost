package wg

import (
	"fmt"
	"net"
	"sync/atomic"
	"syscall"
)

// Проверка перед каждым соединением мимо туннеля.
//
// На iOS и Android собственные сокеты расширения и службы в их же туннель не
// попадают — так устроена система, и проверять нечего. На Windows маршрут по
// умолчанию уходит в интерфейс туннеля для всего компьютера, включая сам
// демон: прямой выход («напрямую» по правилам маршрутизации) ушёл бы в туннель
// вместо физической сети. Демон ставит здесь функцию, которая закрепляет адрес
// за физическим шлюзом до первого пакета, — так же, как транспорт закрепляет
// релеи ВК (proxy.SetDialHook). Без функции путь прежний.
var dialHook atomic.Pointer[func(network, address string) error]

// SetDialHook ставит проверку; nil снимает. Функция получает сеть («tcp4»,
// «udp4», …) и уже разрешённый «адрес:порт» и вызывается из многих горутин.
func SetDialHook(h func(network, address string) error) {
	if h == nil {
		dialHook.Store(nil)
		return
	}
	dialHook.Store(&h)
}

func beforeDial(network, address string) error {
	h := dialHook.Load()
	if h == nil {
		return nil
	}
	if err := (*h)(network, address); err != nil {
		return fmt.Errorf("проверка перед соединением %s %s: %w", network, address, err)
	}
	return nil
}

// dialControl — beforeDial в форме net.Dialer.Control: адрес уже разрешён,
// сокет создан, соединение ещё не начато.
func dialControl(network, address string, _ syscall.RawConn) error {
	return beforeDial(network, address)
}

// beforeSendUDP — то же для неподключённого UDP-сокета, которому адрес
// назначения передаётся при каждой отправке.
func beforeSendUDP(address *net.UDPAddr) error {
	if dialHook.Load() == nil || address == nil {
		return nil
	}
	network := "udp4"
	if address.IP.To4() == nil {
		network = "udp6"
	}
	return beforeDial(network, address.String())
}
