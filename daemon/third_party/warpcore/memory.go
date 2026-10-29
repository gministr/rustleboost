package warpcore

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Дисциплина памяти для расширения туннеля.
//
// NetworkExtension в iOS живёт под жёстким лимитом около 50 МБ, а рантайм Go
// по умолчанию отдаёт память системе неохотно. Без этих настроек расширение
// с ядром упирается в лимит, и его снимает система.
//
// Включается только там, где лимит есть. В процессе приложения те же
// настройки вредны: замер задержки поднимает три десятка исходящих каналов
// разом, и при низком потолке сборщик мусора не даёт работать ничему
// другому — результаты замера приходили с задержкой в полтора десятка секунд
// вместо честных пяти.

const (
	// Ниже этого предела ядро начинает захлёбываться на сборке мусора.
	minMemoryLimitMB = 20
	// Выше — рискуем упереться в жёсткий лимит расширения и быть снятыми
	// системой без предупреждения.
	maxMemoryLimitMB = 50
)

var memoryOnce sync.Once

// freeOSEverySec — как часто страницы возвращаются системе.
var freeOSEverySec atomic.Int64

// memoryLimited — включён ли экономный режим (SetMemoryLimit).
var memoryLimited atomic.Bool

func init() { freeOSEverySec.Store(3) }

// UseSrtpMemoryProfile — режим памяти для SRTP, как в оригинальном
// приложении: потолок 35 МБ, обычная частота сборок, возврат страниц раз в
// 15 секунд: после всплеска (спидтест) расширение должно быстро сдуваться. Жёсткий режим WDTT (20 МБ и уборка каждые 3 с) на 60 соединениях
// давал десятки сборок в секунду, и процессор уходил на них, а не на трафик.
func UseSrtpMemoryProfile() {
	// Только там, где экономный режим включён (расширение iOS): на Android
	// и Windows потолка нет, и лишняя граница только душила бы сборщиком.
	if !memoryLimited.Load() {
		return
	}
	debug.SetGCPercent(100)
	debug.SetMemoryLimit(35 << 20)
	freeOSEverySec.Store(15)
}

// SetMemoryLimit включает экономный режим рантайма с заданным потолком в
// мегабайтах. Вызывается расширением туннеля один раз при запуске;
// приложение его не включает.
//
// Значение зажимается в разумные границы: настройка приходит из интерфейса,
// и промах пользователя не должен превращаться в снятое системой расширение.
func SetMemoryLimit(megabytes int) {
	memoryOnce.Do(func() {
		memoryLimited.Store(true)
		limit := megabytes
		if limit < minMemoryLimitMB {
			limit = minMemoryLimitMB
		}
		if limit > maxMemoryLimitMB {
			limit = maxMemoryLimitMB
		}

		// Держать память обязан потолок, а не частота сборок.
		//
		// Раньше здесь стояло SetGCPercent(10) и FreeOSMemory каждую секунду.
		// Это работало, но дорого: замер под нагрузкой показал больше
		// пятнадцати тысяч сборок за семь минут — около сотни в секунду. Такая
		// частота стоит процессорного времени, а значит скорости и заряда.
		//
		// SetMemoryLimit — жёсткая граница: сборщик сам ускорится, когда
		// куча к ней подойдёт. Поэтому обычный режим можно сделать спокойнее,
		// не рискуя превысить лимит расширения.
		debug.SetGCPercent(50)
		debug.SetMemoryLimit(int64(limit) * 1024 * 1024)

		// Возврат страниц системе — реже: сам по себе он тоже запускает полную
		// сборку, и ежесекундный вызов удваивал нагрузку на ровном месте.
		go func() {
			for {
				time.Sleep(time.Duration(freeOSEverySec.Load()) * time.Second)
				debug.FreeOSMemory()
			}
		}()
	})
}

// FreeMemory немедленно собирает мусор и отдаёт свободные страницы системе.
//
// Вызывается расширением по сигналу системы о нехватке памяти: процесс
// с ядром близок к лимиту, и следующий всплеск соединений закончился бы его
// снятием. Полная сборка стоит миллисекунды — несравнимо дешевле оборванного
// туннеля.
func FreeMemory() {
	debug.FreeOSMemory()
}

// MemStatsJSON — разбивка памяти рантайма Go.
//
// Нужна, чтобы понять, кто занимает те самые 45 МБ, при которых система
// снимает расширение. Общий след процесса измеряется снаружи, а здесь видно
// долю Go: если `sys` заметно меньше следа, остаток приходится на мост
// tun2socks, Swift и сам рантайм, и резать буферы в Go бессмысленно.
//
// `goroutines` показывает утечку горутин: их число должно держаться ровным, а
// не расти с каждым соединением.
func MemStatsJSON() string {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	const mb = 1024 * 1024
	// Сколько Go реально держит в памяти, а не просто зарезервировал: кучу в
	// работе, ещё не отданную системе свободную кучу, стеки и служебное.
	// Отданное системе (HeapReleased) в след процесса не входит.
	resident := stats.HeapInuse + (stats.HeapIdle - stats.HeapReleased) + stats.StackInuse +
		stats.MSpanInuse + stats.MCacheInuse + stats.GCSys + stats.OtherSys + stats.BuckHashSys
	payload, err := json.Marshal(map[string]any{
		"heapMB":      stats.HeapAlloc / mb,
		"heapInuseMB": stats.HeapInuse / mb,
		"heapIdleMB":  (stats.HeapIdle - stats.HeapReleased) / mb,
		"stackMB":     stats.StackInuse / mb,
		"gcSysMB":     stats.GCSys / mb,
		"otherMB":     (stats.OtherSys + stats.MSpanInuse + stats.MCacheInuse + stats.BuckHashSys) / mb,
		"residentMB":  resident / mb,
		"sysMB":       stats.Sys / mb,
		"goroutines":  runtime.NumGoroutine(),
		"numGC":       stats.NumGC,
	})
	if err != nil {
		return "{}"
	}
	return string(payload)
}

// GoroutineCount — текущее число горутин. Дёшево, в отличие от снимка стеков.
func GoroutineCount() int { return runtime.NumGoroutine() }

// GoroutineTopJSON — сводка горутин по месту, где они ждут.
//
// Нужна для поиска утечки: общее число горутин говорит, что течь есть, но не
// говорит где. Здесь стеки группируются по верхнему кадру, и место утечки
// видно сразу — по нему счётчик растёт, а по остальным стоит.
//
// Дороже обычного замера: снимок стеков останавливает рантайм на миллисекунды.
// Поэтому вызывается не каждым пульсом, а только когда горутин подозрительно
// много.
func GoroutineTopJSON(limit int) string {
	buffer := make([]byte, 1<<20)
	written := runtime.Stack(buffer, true)

	counts := map[string]int{}
	for _, block := range strings.Split(string(buffer[:written]), "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		// Первая строка — заголовок горутины, вторая — функция, где она
		// сейчас стоит. Она и интересна.
		if len(lines) < 2 {
			continue
		}
		frame := strings.TrimSpace(lines[1])
		if index := strings.LastIndex(frame, "("); index > 0 {
			frame = frame[:index]
		}
		counts[frame]++
	}

	type entry struct {
		Frame string `json:"frame"`
		Count int    `json:"count"`
	}
	entries := make([]entry, 0, len(counts))
	for frame, count := range counts {
		entries = append(entries, entry{Frame: frame, Count: count})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Count > entries[j].Count })
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	payload, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

// Hysteria2Version — версия вшитой реализации Hysteria2, чтобы её можно было
// показать в разделе «О приложении» рядом с версией Xray.
func Hysteria2Version() string {
	return "2.12.2"
}
