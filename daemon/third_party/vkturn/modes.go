package vkturn

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Режимы работы и канал результата капчи.
//
// В Android-клиенте всё это жило в main.go рядом с разбором флагов. Здесь
// вынесено отдельно, потому что задаётся не аргументами процесса, а вызовами
// из приложения.

var (
	captchaModeValue atomic.Value
	vkAuthModeValue  atomic.Value
)

// CaptchaResultChan — канал, в который попадает разгаданная капча.
//
// Буфер на один элемент и обязательный drain перед каждым запросом: результат
// предыдущей попытки, пришедший с опозданием, иначе был бы принят за ответ на
// следующую.
var CaptchaResultChan = make(chan string, 1)

func init() {
	captchaModeValue.Store("auto")
	vkAuthModeValue.Store("vkcalls")
}

func normalizeCaptchaMode(mode string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(mode)); normalized {
	case "auto", "rjs", "wv":
		return normalized
	default:
		return "auto"
	}
}

func setCaptchaMode(mode string) string {
	normalized := normalizeCaptchaMode(mode)
	captchaModeValue.Store(normalized)
	return normalized
}

func getCaptchaMode() string {
	mode, _ := captchaModeValue.Load().(string)
	if mode == "" {
		return "auto"
	}
	return mode
}

func normalizeVKAuthMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "legacy":
		return "legacy"
	default:
		return "vkcalls"
	}
}

func setVKAuthMode(mode string) string {
	normalized := normalizeVKAuthMode(mode)
	vkAuthModeValue.Store(normalized)
	return normalized
}

func getVKAuthMode() string {
	mode, _ := vkAuthModeValue.Load().(string)
	if mode == "" {
		return "vkcalls"
	}
	return mode
}

func drainCaptchaResult() {
	select {
	case <-CaptchaResultChan:
	default:
	}
}

// CaptchaHandler вызывается, когда Go-решателю не удалось разобрать капчу и
// нужен браузер.
//
// На Android эту роль играл WebView в приложении: клиент печатал запрос в
// stdout, Kotlin открывал окно и возвращал ответ через stdin. На iOS
// расширение туннеля окон не показывает вовсе, поэтому запрос уходит
// обработчику, а тот волен переадресовать его приложению — если оно в этот
// момент на экране.
//
// Обработчик не установлен — путь через браузер просто недоступен, и остаются
// Go-решатель и анонимный режим VK Calls. Это штатная ситуация, а не ошибка:
// вызов должен вернуться сразу, иначе воркер встанет на таймаут вместо того,
// чтобы пробовать следующий способ.
type CaptchaHandler func(mode, redirectURI, sessionToken string)

var (
	captchaHandlerMu sync.RWMutex
	captchaHandler   CaptchaHandler
)

// SetCaptchaHandler задаёт обработчик. nil отключает путь через браузер.
func SetCaptchaHandler(handler CaptchaHandler) {
	captchaHandlerMu.Lock()
	defer captchaHandlerMu.Unlock()
	captchaHandler = handler
}

// hasCaptchaHandler сообщает, есть ли кому показать капчу.
func hasCaptchaHandler() bool {
	captchaHandlerMu.RLock()
	defer captchaHandlerMu.RUnlock()
	return captchaHandler != nil
}

// requestCaptcha передаёт запрос обработчику.
func requestCaptcha(mode, redirectURI, sessionToken string) {
	captchaHandlerMu.RLock()
	handler := captchaHandler
	captchaHandlerMu.RUnlock()
	if handler == nil {
		return
	}
	// В отдельной горутине: обработчик уходит в приложение через границу
	// процессов, и блокировать на этом воркер нельзя — он ждёт результат по
	// каналу со своим таймаутом.
	go handler(mode, redirectURI, sessionToken)
}

// SubmitCaptcha отдаёт результат ожидающему воркеру.
//
// Строка "error:timeout" или "error:cancelled" сообщает, что пользователь не
// справился или закрыл окно: воркер тогда не ждёт до конца таймаута.
func SubmitCaptcha(result string) {
	drainCaptchaResult()
	select {
	case CaptchaResultChan <- result:
	default:
	}
}
