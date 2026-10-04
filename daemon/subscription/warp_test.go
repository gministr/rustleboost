package subscription

import "testing"

func TestParseWdttLink(t *testing.T) {
	s, err := parseWdtt("wdtt://secret@151.243.208.197:56000?hashes=h1,h2&workers=16&obfs=video#Финляндия")
	if err != nil {
		t.Fatal(err)
	}
	if s.Engine != EngineWdtt || s.Address != "151.243.208.197" || s.Port != 56000 {
		t.Fatalf("неверный сервер: %+v", s)
	}
	if s.Params["password"] != "secret" || s.Params["hashes"] != "h1,h2" || s.Params["obfs"] != "video" {
		t.Fatalf("параметры: %+v", s.Params)
	}
	if s.Name != "Финляндия" {
		t.Fatalf("имя: %q", s.Name)
	}
}

func TestParseWdttRejectsIncomplete(t *testing.T) {
	for _, link := range []string{
		"wdtt://151.243.208.197:56000?hashes=h1",  // нет пароля
		"wdtt://secret@151.243.208.197:56000",     // нет хешей
		"wdtt://secret@151.243.208.197?hashes=h1", // нет порта
	} {
		if _, err := parseWdtt(link); err == nil {
			t.Errorf("ссылка принята без проверки: %s", link)
		}
	}
}

func TestWdttLinkIsRecognisedInSubscription(t *testing.T) {
	if !looksLikeProxyURI("wdtt://secret@151.243.208.197:56000?hashes=h1") {
		t.Fatal("ссылка wdtt:// не распознана при разборе подписки")
	}
}

func TestShortKeyIsLastPathSegment(t *testing.T) {
	if got := ShortKey("https://sub.lindavpn.com/abc123/"); got != "abc123" {
		t.Fatalf("ключ: %q", got)
	}
}

func TestWarpServerIsSyntheticWarpEngine(t *testing.T) {
	s := WarpServer()
	if s.Engine != EngineWarp || s.Protocol != "WARP" {
		t.Fatalf("пункт WARP: %+v", s)
	}
}
