package vkturn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Креды TURN на диске.
//
// Кэш в памяти пропадает вместе с процессом. А процесс расширения туннеля iOS
// снимает без предупреждения — при нехватке памяти, при обновлении системы, —
// и система тут же поднимает его заново. Новый процесс шёл к VK за кредами с
// нуля, и VK отвечал капчей: частые запросы кредов с одного адреса он
// принимает за перебор. Капчу в расширении решить нечем — окна для неё нет, —
// и туннель не поднимался вовсе.
//
// На устройстве так и было: расширение сняли, через сорок секунд оно
// поднялось, запросило креды, получило капчу и осталось без связи. Креды при
// этом живут десять минут, и прежние ещё годились.
//
// Хранятся в каталоге кэша самого расширения, с правами только для владельца.
// Это не долговечный секрет: срок жизни — минуты, и истёкшие не читаются.

var (
	credsDirMu  sync.RWMutex
	credsDir    string
	credsFileMu sync.Mutex
)

// setCredentialsDirectory задаёт каталог для кредов. Пусто — только память.
func setCredentialsDirectory(dir string) {
	credsDirMu.Lock()
	credsDir = dir
	credsDirMu.Unlock()
}

type storedCredentials struct {
	Username    string    `json:"username"`
	Password    string    `json:"password"`
	ServerAddrs []string  `json:"serverAddrs"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// credentialsFile — один файл на пару «ссылка звонка, номер кэша». Имя —
// отпечаток, а не сама ссылка: хеш звонка незачем держать открытым текстом в
// имени файла.
func credentialsFile(link string, cacheID int) string {
	credsDirMu.RLock()
	dir := credsDir
	credsDirMu.RUnlock()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(link))
	name := "turn-" + hex.EncodeToString(sum[:8]) + "-" + strconv.Itoa(cacheID) + ".json"
	return filepath.Join(dir, name)
}

func loadCredentialsFromDisk(link string, cacheID int) (TurnCredentials, bool) {
	path := credentialsFile(link, cacheID)
	if path == "" {
		return TurnCredentials{}, false
	}
	credsFileMu.Lock()
	data, err := os.ReadFile(path)
	credsFileMu.Unlock()
	if err != nil {
		return TurnCredentials{}, false
	}
	var stored storedCredentials
	if json.Unmarshal(data, &stored) != nil {
		return TurnCredentials{}, false
	}
	if !time.Now().Before(stored.ExpiresAt) || len(stored.ServerAddrs) == 0 ||
		stored.Username == "" {
		removeCredentialsFromDisk(link, cacheID)
		return TurnCredentials{}, false
	}
	return TurnCredentials{
		Username:    stored.Username,
		Password:    stored.Password,
		ServerAddrs: stored.ServerAddrs,
		ExpiresAt:   stored.ExpiresAt,
		Link:        link,
	}, true
}

func saveCredentialsToDisk(creds TurnCredentials, cacheID int) {
	path := credentialsFile(creds.Link, cacheID)
	if path == "" {
		return
	}
	data, err := json.Marshal(storedCredentials{
		Username:    creds.Username,
		Password:    creds.Password,
		ServerAddrs: creds.ServerAddrs,
		ExpiresAt:   creds.ExpiresAt,
	})
	if err != nil {
		return
	}
	credsFileMu.Lock()
	defer credsFileMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	temporary := path + ".tmp"
	if os.WriteFile(temporary, data, 0o600) == nil {
		_ = os.Rename(temporary, path)
	}
}

func removeCredentialsFromDisk(link string, cacheID int) {
	path := credentialsFile(link, cacheID)
	if path == "" {
		return
	}
	credsFileMu.Lock()
	_ = os.Remove(path)
	credsFileMu.Unlock()
}
