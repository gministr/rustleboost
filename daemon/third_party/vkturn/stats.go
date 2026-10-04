package vkturn

import (
	"log"
	"sync/atomic"
	"time"
)

type Stats struct {
	TotalBytesUp      atomic.Int64
	TotalBytesDown    atomic.Int64
	ActiveConnections atomic.Int32
	// DroppedUp — исходящие пакеты, которые некому было отдать: очереди всех
	// воркеров заполнены. Каждый такой пакет — потеря для TCP внутри
	// туннеля, и по этому счётчику видно, упирается ли отдача в воркеры.
	DroppedUp atomic.Int64
}

func NewStats() *Stats {
	return &Stats{}
}

const statsInterval = 3 * time.Second

// RunLoop пишет скорость в журнал — но только когда что-то происходит.
// Раньше строка писалась каждые три секунды и в простое, и журнал движка на
// тысячу строк почти целиком состоял из одинаковых «Трафик: 0.01 МБ».
func (s *Stats) RunLoop(shutdown <-chan struct{}) {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()

	var lastUp, lastDown, lastDropped int64
	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			up := s.TotalBytesUp.Load()
			down := s.TotalBytesDown.Load()
			dropped := s.DroppedUp.Load()
			if up != lastUp || down != lastDown || dropped != lastDropped {
				seconds := statsInterval.Seconds()
				log.Printf("[СТАТИСТИКА] Активных: %d | ↑ %.2f Мбит/с ↓ %.2f Мбит/с | сброшено ↑ %d",
					s.ActiveConnections.Load(),
					float64(up-lastUp)*8/seconds/1e6,
					float64(down-lastDown)*8/seconds/1e6,
					dropped-lastDropped)
				lastUp, lastDown, lastDropped = up, down, dropped
			}
			emitStats(s)
		}
	}
}
