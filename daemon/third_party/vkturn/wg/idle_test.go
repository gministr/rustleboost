package wg

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// relay() раньше жил без ограничения по времени после рукопожатия. Если одна
// из сторон переставала отвечать, не закрывая соединение, — частый случай на
// переходе через TURN-релей при потере пакета с FIN, — обе горутины io.Copy
// блокировались навсегда, а вместе с ними не освобождался слот из
// maxConcurrentConnections.
//
// Подтверждено дампом горутин на живом устройстве: 247 блокированы на чтении
// из настоящего сокета, 127 — на чтении из сетевого стека.

// withDeadline подменяет idleTimeout на время теста, чтобы не ждать девяносто
// секунд ради проверки самого механизма.
func withDeadline(t *testing.T, timeout time.Duration) {
	t.Helper()
	original := idleTimeout
	idleTimeout = timeout
	t.Cleanup(func() { idleTimeout = original })
}

// TestRelayClosesWhenBothSidesGoSilent — главный тест на дефект: ни клиент,
// ни сервер не пишут и не закрывают соединение сами. Раньше relay() не
// возвращался бы никогда; теперь обязан вернуться сразу за idleTimeout.
func TestRelayClosesWhenBothSidesGoSilent(t *testing.T) {
	withDeadline(t, 30*time.Millisecond)

	clientSide, clientPeer := net.Pipe()
	remoteSide, remotePeer := net.Pipe()
	defer clientPeer.Close()
	defer remotePeer.Close()

	// Пиры подтверждают, что relay() закрыл свои концы, а не
	// завис: без этого net.Pipe() блокировал бы чтение вечно сам по себе.
	go func() { _, _ = io.Copy(io.Discard, clientPeer) }()
	go func() { _, _ = io.Copy(io.Discard, remotePeer) }()

	done := make(chan struct{})
	go func() {
		relay(clientSide, nil, func() (net.Conn, error) { return remoteSide, nil })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay() не вернулся — соединение без активности повисло")
	}
}

// TestRelayKeepsAliveDuringTrickleTraffic — обратная сторона того же
// механизма: редкие, но живые данные не должны обрывать соединение. Иначе
// вместо утечки получилось бы разорванное скачивание.
func TestRelayKeepsAliveDuringTrickleTraffic(t *testing.T) {
	withDeadline(t, 60*time.Millisecond)

	clientSide, clientPeer := net.Pipe()
	remoteSide, remotePeer := net.Pipe()
	defer clientPeer.Close()

	done := make(chan struct{})
	go func() {
		relay(clientSide, nil, func() (net.Conn, error) { return remoteSide, nil })
		close(done)
	}()

	// Читаем то, что relay() пересылает от remotePeer к clientPeer.
	go func() { _, _ = io.Copy(io.Discard, clientPeer) }()

	// Пишем в remotePeer реже, чем сработал бы старый общий таймаут на
	// рукопожатие (10 с), но чаще, чем idleTimeout теста (60 мс) — то есть
	// ровно так, как выглядит медленная, но живая передача.
	total := 6
	for i := 0; i < total; i++ {
		if _, err := remotePeer.Write([]byte("x")); err != nil {
			t.Fatalf("запись %d: %v", i, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case <-done:
		t.Fatal("relay() завершился раньше времени — живое соединение приняли за брошенное")
	default:
	}

	remotePeer.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay() не завершился после закрытия remotePeer")
	}
}

// TestIdleConnExtendsDeadlineOnRead проверяет сам механизм продления: срок
// сдвигается вперёд после каждого успешного чтения.
func TestIdleConnExtendsDeadlineOnRead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	timeout := 40 * time.Millisecond
	conn := idleConn{server, timeout}
	_ = server.SetDeadline(time.Now().Add(timeout))

	// Пишем чаще, чем срок, дольше, чем сам срок в одиночку позволил бы
	// прожить, — если продление не работает, чтение оборвётся раньше.
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(15 * time.Millisecond)
			_, _ = client.Write([]byte("y"))
		}
	}()

	buffer := make([]byte, 1)
	deadlineStarted := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := conn.Read(buffer); err != nil {
			t.Fatalf("чтение %d прервалось раньше времени за %v: %v", i, time.Since(deadlineStarted), err)
		}
	}
}

// TestIdleConnTimesOutWithoutActivity — без продления чтение обязано
// оборваться по истечении срока, а не повиснуть.
func TestIdleConnTimesOutWithoutActivity(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	timeout := 30 * time.Millisecond
	conn := idleConn{server, timeout}
	_ = server.SetDeadline(time.Now().Add(timeout))

	buffer := make([]byte, 1)
	_, err := conn.Read(buffer)
	if err == nil {
		t.Fatal("чтение не вернуло ошибку без единого байта активности")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("ожидалась ошибка таймаута, получено: %v", err)
	}
}

// TestRelayDeliversResponseAfterClientHalfClose — клиент отправляет запрос и
// закрывает свою сторону на запись; сервер отвечает, лишь дочитав запрос до
// конца. Раньше пересылка, увидев конец запроса, закрывала соединение
// целиком, и ответ терялся.
func TestRelayDeliversResponseAfterClientHalfClose(t *testing.T) {
	withDeadline(t, 2*time.Second)

	listen := func() net.Listener {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	// «Сервер назначения»: читает запрос до EOF и только потом отвечает.
	remoteListener := listen()
	defer remoteListener.Close()
	go func() {
		conn, err := remoteListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		request, _ := io.ReadAll(conn)
		_, _ = conn.Write(append([]byte("ответ на "), request...))
	}()

	// Пересылка между клиентом и сервером назначения.
	relayListener := listen()
	defer relayListener.Close()
	go func() {
		client, err := relayListener.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		relay(client, nil, func() (net.Conn, error) { return net.Dial("tcp", remoteListener.Addr().String()) })
	}()

	conn, err := net.Dial("tcp", relayListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("запрос")); err != nil {
		t.Fatal(err)
	}
	_ = conn.(*net.TCPConn).CloseWrite()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	response, err := io.ReadAll(conn)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("чтение ответа: %v", err)
	}
	if string(response) != "ответ на запрос" {
		t.Fatalf("ответ потерян: %q", response)
	}
}
