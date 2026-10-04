package vkturn

import (
	"encoding/json"
	"log"
)

type eventType string

const (
	eventStarted        eventType = "STARTED"
	eventStopped        eventType = "STOPPED"
	eventReady          eventType = "READY"
	eventConfig         eventType = "CONFIG"
	eventStats          eventType = "STATS"
	eventError          eventType = "ERROR"
	eventCaptchaRequest eventType = "CAPTCHA_REQUEST"
	eventCaptchaDone    eventType = "CAPTCHA_DONE"
)

// emitEvent отправляет событие приёмнику, заданному SetEventHandler.
//
// Оригинал печатал события в stdout под флагом WDTT_EVENTS: их читал
// родительский процесс на Android. Здесь родительского процесса нет, и роль
// флага играет сам приёмник — пока он не задан, события никуда не идут.
func emitEvent(t eventType, payload map[string]any) {
	var p []byte
	if len(payload) > 0 {
		var err error
		p, err = json.Marshal(payload)
		if err != nil {
			log.Printf("[EVENT] failed to marshal %s event: %v", t, err)
			return
		}
	}
	dispatchEvent(string(t), string(p))
}

func emitError(code, message string, fatal bool) {
	emitEvent(eventError, map[string]any{
		"code":    code,
		"message": message,
		"fatal":   fatal,
	})
}

func emitStats(s *Stats) {
	emitEvent(eventStats, map[string]any{
		"active":     s.ActiveConnections.Load(),
		"bytes_up":   s.TotalBytesUp.Load(),
		"bytes_down": s.TotalBytesDown.Load(),
	})
}

func emitReady() {
	emitEvent(eventReady, nil)
}

func emitConfig(config string) {
	emitEvent(eventConfig, map[string]any{"config": config})
}

func emitCaptchaRequest(mode, redirectURI, sessionToken string) {
	emitEvent(eventCaptchaRequest, map[string]any{
		"mode":          mode,
		"redirect_uri":  redirectURI,
		"session_token": sessionToken,
	})
}

func emitCaptchaDone(success bool, err string) {
	payload := map[string]any{"success": success}
	if err != "" {
		payload["error"] = err
	}
	emitEvent(eventCaptchaDone, payload)
}
