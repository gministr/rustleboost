package vkturn

import (
	"bytes"
	"log"
	"sync"
)

// Каналы наружу: события и лог.
//
// Android-клиент — отдельный процесс, поэтому оригинал печатал события
// строками в stdout, а Kotlin разбирал их построчно. Внутри расширения
// туннеля stdout не читает никто, так что оба потока превращены в то, что
// можно забрать вызовом: события — в обработчик, лог — в кольцевой буфер.

// EventHandler получает события клиента: kind — одно из STARTED, STOPPED,
// READY, CONFIG, STATS, ERROR, CAPTCHA_REQUEST, CAPTCHA_DONE; payload — JSON
// или пустая строка.
type EventHandler func(kind, payload string)

var (
	eventHandlerMu sync.RWMutex
	eventHandler   EventHandler
)

// SetEventHandler задаёт приёмник событий. nil отключает их.
func SetEventHandler(handler EventHandler) {
	eventHandlerMu.Lock()
	defer eventHandlerMu.Unlock()
	eventHandler = handler
}

func dispatchEvent(kind, payload string) {
	eventHandlerMu.RLock()
	handler := eventHandler
	eventHandlerMu.RUnlock()
	if handler == nil {
		return
	}
	handler(kind, payload)
}

// Размер кольца подобран под разбор одного неудачного подключения: при 108
// воркерах старт укладывается в пару сотен строк, и кольцо покрывает попытку
// целиком вместе с предысторией.
const logRingLines = 2000

var logRing = &ringBuffer{lines: make([]string, 0, logRingLines)}

// ringBuffer хранит последние строки лога.
//
// Ограничение по числу строк, а не по байтам: расширение туннеля живёт под
// потолком памяти в 50 МБ, и неограниченный лог съел бы его быстрее самого
// транспорта.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	start int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	line := string(bytes.TrimRight(p, "\n"))
	if line == "" {
		return len(p), nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.lines) < logRingLines {
		r.lines = append(r.lines, line)
	} else {
		r.lines[r.start] = line
		r.start = (r.start + 1) % logRingLines
	}
	return len(p), nil
}

func (r *ringBuffer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, 0, len(r.lines))
	for i := 0; i < len(r.lines); i++ {
		out = append(out, r.lines[(r.start+i)%len(r.lines)])
	}
	return out
}

func init() {
	// Отметки времени с миллисекундами. Журнал движка сохраняется в файл как
	// есть, минуя систему логирования Swift, и без них не понять, на что
	// уходят секунды запуска: получение кредов, аллокации, ответ сервера.
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(logRing)
}

// ReadLog отдаёт накопленный лог одной строкой.
func ReadLog() string {
	lines := logRing.snapshot()
	var out bytes.Buffer
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(line)
	}
	return out.String()
}
