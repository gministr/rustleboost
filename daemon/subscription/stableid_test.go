package subscription

import "testing"

// Идентификатор сервера не меняется от обновления к обновлению подписки:
// иначе интерфейс с прежним списком получал «server … not found».
func TestStableIDsSurviveRefresh(t *testing.T) {
	list := func() []Server {
		return []Server{
			{ID: newID(), Group: GroupRegular, Name: "США 🟢", Address: "alb.example.org", Port: 8443, Protocol: "Trojan"},
			{ID: newID(), Group: GroupRegular, Name: "Финляндия", Address: "fi.example.org", Port: 443, Protocol: "VLESS"},
			{ID: newID(), Group: GroupRegular, Name: "Финляндия", Address: "fi.example.org", Port: 443, Protocol: "VLESS"},
		}
	}
	first, second := list(), list()
	AssignStableIDs(first)
	AssignStableIDs(second)
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("сервер %d сменил идентификатор после обновления", i)
		}
	}
	if first[1].ID == first[2].ID {
		t.Fatal("два одинаковых пункта получили один идентификатор")
	}
}
