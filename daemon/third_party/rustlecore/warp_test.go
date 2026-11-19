package rustlecore

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Ключи должны быть настоящей парой curve25519 в base64 — иначе Cloudflare
// отвергнет регистрацию, а разбираться придётся по невнятному отказу.
func TestWarpKeyPair(t *testing.T) {
	priv, pub, err := warpKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{"закрытый": priv, "открытый": pub} {
		raw, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			t.Fatalf("%s ключ не в base64: %v", name, err)
		}
		if len(raw) != 32 {
			t.Fatalf("%s ключ длиной %d байт, ожидалось 32", name, len(raw))
		}
	}
	if priv == pub {
		t.Fatal("закрытый и открытый ключи совпали")
	}
	// Каждый вызов даёт новую пару.
	priv2, _, _ := warpKeyPair()
	if priv == priv2 {
		t.Fatal("две регистрации подряд получили один ключ")
	}
}

// Регистрация сохраняется на устройстве: повторный запуск не должен заводить
// в Cloudflare новое устройство на каждое подключение.
func TestWarpCredentialsAreReused(t *testing.T) {
	dir := t.TempDir()
	saved := warpCredentials{
		PrivateKey: "aaa", Address: "172.16.0.2/32",
		PeerPublicKey: "bbb", Endpoint: "162.159.192.1:2408", Upstream: "162.159.192.1:2408",
	}
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(filepath.Join(dir, warpCredentialsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := warpEnsureCredentials(dir, "", "", false)
	if err != nil {
		t.Fatalf("сохранённая регистрация не прочиталась: %v", err)
	}
	if got.PeerPublicKey != saved.PeerPublicKey || got.Address != saved.Address {
		t.Fatalf("прочиталось не то: %+v", got)
	}

	// Сброс заставляет зарегистрироваться заново.
	WarpReset(dir)
	if _, err := os.Stat(filepath.Join(dir, warpCredentialsFile)); !os.IsNotExist(err) {
		t.Fatal("после сброса регистрация осталась на диске")
	}
}

// Неполная запись не должна сходить за годную: по ней Cloudflare откажет, и
// причину отказа потом не распутать.
func TestWarpBrokenCredentialsRejected(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"пустой закрытый ключ": `{"privateKey":"","peerPublicKey":"b","address":"1.2.3.4/32","endpoint":"1.1.1.1:2408","upstream":"1.1.1.1:2408"}`,
		"нет узла":             `{"privateKey":"a","address":"1.2.3.4/32","endpoint":"1.1.1.1:2408","upstream":"1.1.1.1:2408"}`,
		"нет адреса":           `{"privateKey":"a","peerPublicKey":"b","endpoint":"1.1.1.1:2408","upstream":"1.1.1.1:2408"}`,
		"не разобралось":       `{сломано`,
	}
	for name, body := range cases {
		path := filepath.Join(dir, name+".json")
		_ = os.WriteFile(path, []byte(body), 0o600)
		if _, ok := warpLoadCredentials(path); ok {
			t.Errorf("%s: запись принята как годная", name)
		}
	}
	// Полная запись принимается.
	path := filepath.Join(dir, "good.json")
	_ = os.WriteFile(path, []byte(`{"privateKey":"a","peerPublicKey":"b","address":"1.2.3.4/32","endpoint":"1.1.1.1:2408","upstream":"1.1.1.1:2408"}`), 0o600)
	if _, ok := warpLoadCredentials(path); !ok {
		t.Error("полная запись не принята")
	}
}

// Сохранённую регистрацию мы держим, а вот адрес узла переспрашиваем: он
// меняется при переключении на ретранслятор. Проверяем, что новый адрес
// подхватывается, а ключи при этом остаются прежние.
func TestWarpEndpointRefreshedForSavedCredentials(t *testing.T) {
	relay := []string{"151.243.208.197:3478", "151.243.208.197:4500"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/warp/endpoint" {
			t.Errorf("спросили не тот адрес: %s", r.URL.Path)
		}
		if r.Header.Get("X-App-Key") != "ключ" {
			t.Error("запрос ушёл без ключа приложения")
		}
		_, _ = w.Write([]byte(`{"endpoints":["` + relay[0] + `","` + relay[1] + `"]}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, warpCredentialsFile)
	saved := `{"privateKey":"ключ","peerPublicKey":"узел","address":"172.16.0.2/32","endpoint":"162.159.192.7:2408","upstream":"162.159.192.7:2408"}`
	_ = os.WriteFile(path, []byte(saved), 0o600)

	creds, err := warpEnsureCredentials(dir, server.URL+"/v1/warp/register", "ключ", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(creds.Endpoints, relay) {
		t.Errorf("список адресов не обновился: %q", creds.Endpoints)
	}
	// Прежний адрес из списка выпал, поэтому перебор должен начинаться с
	// первого нового, а не со старого, который уже не предлагают.
	if creds.Endpoint != relay[0] {
		t.Errorf("адрес не обновился: %q", creds.Endpoint)
	}
	if creds.PrivateKey != "ключ" {
		t.Errorf("ключ подменился: %q", creds.PrivateKey)
	}
	// Обновление должно лечь на диск, иначе следующий запуск спросит снова
	// и без сети вернётся к недоступному адресу.
	if again, ok := warpLoadCredentials(path); !ok || !slices.Equal(again.Endpoints, relay) {
		t.Errorf("новый список не сохранён: %+v", again)
	}
}

// Проверенный на этой сети адрес остаётся первым, пока сервер его предлагает:
// иначе перебор начинался бы заново на каждом подключении.
func TestWarpWorkingEndpointStaysFirst(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"endpoints":["151.243.208.197:3478","151.243.208.197:4500"]}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	saved := `{"privateKey":"ключ","peerPublicKey":"узел","address":"172.16.0.2/32","endpoint":"151.243.208.197:4500","upstream":"162.159.192.7:2408"}`
	_ = os.WriteFile(filepath.Join(dir, warpCredentialsFile), []byte(saved), 0o600)

	creds, err := warpEnsureCredentials(dir, server.URL+"/v1/warp/register", "ключ", true)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Endpoint != "151.243.208.197:4500" {
		t.Errorf("удачный адрес не удержался: %q", creds.Endpoint)
	}
}

// На самом запуске туннеля к серверу ходить нельзя: сетевые настройки уже
// применены, запрос уходит в ещё не построенный туннель и только задерживает
// подключение на время ожидания. Адрес к этому моменту уже спрошен.
func TestWarpStartDoesNotAskServer(t *testing.T) {
	asked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		_, _ = w.Write([]byte(`{"endpoint":"151.243.208.197:443"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, warpCredentialsFile)
	saved := `{"privateKey":"ключ","peerPublicKey":"узел","address":"172.16.0.2/32","endpoint":"162.159.192.7:2408","upstream":"162.159.192.7:2408"}`
	_ = os.WriteFile(path, []byte(saved), 0o600)

	if _, err := warpEnsureCredentials(dir, server.URL+"/v1/warp/register", "ключ", false); err != nil {
		t.Fatal(err)
	}
	if asked != 0 {
		t.Errorf("сервер спросили %d раз(а), а на запуске спрашивать нельзя", asked)
	}
}

// Недоступный сервер не должен мешать подключению: берём сохранённый адрес.
func TestWarpEndpointRefreshFallsBackToSaved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, warpCredentialsFile)
	saved := `{"privateKey":"ключ","peerPublicKey":"узел","address":"172.16.0.2/32","endpoint":"162.159.192.7:2408","upstream":"162.159.192.7:2408"}`
	_ = os.WriteFile(path, []byte(saved), 0o600)

	// Порт, на котором заведомо никто не слушает: сервер поднят и сразу снят.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/v1/warp/register"
	server.Close()

	creds, err := warpEnsureCredentials(dir, url, "ключ", true)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Endpoint != "162.159.192.7:2408" {
		t.Errorf("сохранённый адрес не пригодился: %q", creds.Endpoint)
	}
}

// Пустой порт SOCKS5 — ошибка, а не молчаливый запуск в никуда.
func TestWarpStartRequiresSocksPort(t *testing.T) {
	if err := WarpStart(`{"stateDir":"/tmp"}`); err == nil {
		t.Fatal("запуск без порта SOCKS5 не дал ошибки")
	}
}
