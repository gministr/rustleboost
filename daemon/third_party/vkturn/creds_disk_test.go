package vkturn

import (
	"os"
	"testing"
	"time"
)

// Креды на диске нужны ради одного: процесс расширения сняли, система его
// тут же подняла — и новый процесс не должен идти к VK за кредами, пока
// прежние ещё годны. Иначе VK отвечает капчей, а решить её в расширении нечем.

func withCredentialsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setCredentialsDirectory(dir)
	t.Cleanup(func() { setCredentialsDirectory("") })
	return dir
}

func sampleCredentials(link string, expires time.Time) TurnCredentials {
	return TurnCredentials{
		Username:    "user",
		Password:    "pass",
		ServerAddrs: []string{"1.2.3.4:3478", "5.6.7.8:3478"},
		ExpiresAt:   expires,
		Link:        link,
	}
}

func TestCredentialsSurviveProcessRestart(t *testing.T) {
	withCredentialsDir(t)
	saved := sampleCredentials("hash-a", time.Now().Add(5*time.Minute))
	saveCredentialsToDisk(saved, 0)

	loaded, ok := loadCredentialsFromDisk("hash-a", 0)
	if !ok {
		t.Fatal("сохранённые креды не прочитались")
	}
	if loaded.Username != "user" || loaded.Password != "pass" || len(loaded.ServerAddrs) != 2 {
		t.Fatalf("креды искажены: %+v", loaded)
	}
}

// Истёкшие креды VK отвергнет, а попытка с ними — лишний круг до отказа.
func TestExpiredCredentialsAreNotLoaded(t *testing.T) {
	withCredentialsDir(t)
	saveCredentialsToDisk(sampleCredentials("hash-a", time.Now().Add(-time.Second)), 0)

	if _, ok := loadCredentialsFromDisk("hash-a", 0); ok {
		t.Fatal("истёкшие креды прочитались")
	}
	if path := credentialsFile("hash-a", 0); fileExists(path) {
		t.Fatal("истёкший файл не удалён")
	}
}

// Креды привязаны к звонку и к номеру кэша: чужие не подходят.
func TestCredentialsAreKeyedByLinkAndCache(t *testing.T) {
	withCredentialsDir(t)
	saveCredentialsToDisk(sampleCredentials("hash-a", time.Now().Add(5*time.Minute)), 0)

	if _, ok := loadCredentialsFromDisk("hash-b", 0); ok {
		t.Error("креды другого звонка подошли")
	}
	if _, ok := loadCredentialsFromDisk("hash-a", 1); ok {
		t.Error("креды другого кэша подошли")
	}
}

// Сброшенные после отказов VK креды не должны вернуться с диска при
// перезапуске — иначе новый процесс упрётся в тот же отказ.
func TestInvalidatedCredentialsAreRemovedFromDisk(t *testing.T) {
	withCredentialsDir(t)
	const streamID = 0
	cache := getStreamCache(streamID)
	cache.mutex.Lock()
	cache.creds = sampleCredentials("hash-a", time.Now().Add(5*time.Minute))
	cache.mutex.Unlock()
	saveCredentialsToDisk(cache.creds, getCacheID(streamID))

	cache.invalidate(streamID)

	if _, ok := loadCredentialsFromDisk("hash-a", getCacheID(streamID)); ok {
		t.Fatal("сброшенные креды остались на диске")
	}
}

func TestNoDirectoryMeansMemoryOnly(t *testing.T) {
	setCredentialsDirectory("")
	saveCredentialsToDisk(sampleCredentials("hash-a", time.Now().Add(5*time.Minute)), 0)
	if _, ok := loadCredentialsFromDisk("hash-a", 0); ok {
		t.Fatal("без каталога креды не должны сохраняться")
	}
}

func TestCredentialsFileIsOwnerOnly(t *testing.T) {
	withCredentialsDir(t)
	saveCredentialsToDisk(sampleCredentials("hash-a", time.Now().Add(5*time.Minute)), 0)
	info, err := os.Stat(credentialsFile("hash-a", 0))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("права файла %o, ожидались 600", info.Mode().Perm())
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
