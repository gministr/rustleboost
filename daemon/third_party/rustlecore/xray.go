package rustlecore

import (
	"errors"
	"runtime/debug"
	"sync"

	"github.com/xtls/xray-core/core"

	// Регистрирует все протоколы и транспорты Xray. Без этого импорта ядро
	// собирается пустым и не понимает ни vless, ни reality, ни xhttp.
	_ "github.com/xtls/xray-core/main/distro/all"
)

var (
	xrayMutex    sync.Mutex
	xrayInstance *core.Instance
)

// XrayStart поднимает Xray-core с переданной конфигурацией.
//
// Конфигурация принимается строкой, а не путём к файлу: в ней учётные данные,
// и класть их на диск нельзя.
func XrayStart(configJSON string) error {
	xrayMutex.Lock()
	defer xrayMutex.Unlock()
	if xrayInstance != nil {
		return errors.New("xray: ядро уже запущено")
	}



	// Конфигурация грузится штатным загрузчиком ядра, а не разбором JSON
	// вручную: загрузчик доводит до конца всё, что ядро ждёт от конфига
	// (в частности, поле extra у XHTTP), и без него часть транспортов
	// собиралась в неработающем виде.
	instance, err := core.StartInstance("json", []byte(configJSON))
	if err != nil {
		return err
	}
	xrayInstance = instance
	debug.FreeOSMemory()
	return nil
}

// XrayStop останавливает ядро. Вызов на остановленном ядре безвреден.
func XrayStop() {
	xrayMutex.Lock()
	defer xrayMutex.Unlock()
	if xrayInstance == nil {
		return
	}
	_ = xrayInstance.Close()
	xrayInstance = nil
	// Память старого ядра отдаётся системе сразу, а не когда-нибудь.
	//
	// Остановка почти всегда идёт перед новым запуском (перезапуск ядра
	// сторожем живости), а новый запуск сам разбирает конфиг с десятками
	// тысяч правил. Без этого сборщик мусора не успевал отдать прежнюю кучу,
	// и на перезапуске процесс на мгновение держал два ядра сразу —
	// ровно в этот момент система и снимала расширение по лимиту памяти.
	debug.FreeOSMemory()
}

// XrayIsRunning сообщает, работает ли ядро.
func XrayIsRunning() bool {
	xrayMutex.Lock()
	defer xrayMutex.Unlock()
	return xrayInstance != nil
}

// XrayVersion возвращает версию вшитого ядра.
func XrayVersion() string {
	return core.Version()
}
