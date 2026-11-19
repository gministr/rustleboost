package rustlecore

import "testing"

// Версия ядра проверяется тестом, потому что она попадает в отчёты о работе
// туннеля: расхождение с ожидаемым тегом означает, что собралось не то.
func TestXrayVersion(t *testing.T) {
	if got := XrayVersion(); got != "26.7.28" {
		t.Fatalf("версия ядра %q, ожидалась 26.7.28", got)
	}
}
