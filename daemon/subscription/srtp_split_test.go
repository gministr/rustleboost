package subscription

import "testing"

func TestSplitSRTPLikeIOS(t *testing.T) {
	srtp, err := parseWdtt("wdtt://pass@1.2.3.4:56000?hashes=a&srtp=56100&srtpkey=KEY&srtptok=TOK&relays=9.9.9.9,8.8.8.8:3478#🇹🇷 Турция")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := parseWdtt("wdtt://pass@5.6.7.8:56000?hashes=a#🇩🇪 Германия")
	if err != nil {
		t.Fatal(err)
	}
	named, err := parseWdtt("wdtt://pass@9.8.7.6:56000?hashes=a&srtp=56100&srtpkey=K&srtptok=T&srtpname=%F0%9F%87%AB%F0%9F%87%AE+%D0%A4%D0%B8%D0%BD%D0%BB%D1%8F%D0%BD%D0%B4%D0%B8%D1%8F+%E2%9A%A1%EF%B8%8F#🇫🇮 Финляндия")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Server{&srtp, &plain, &named} {
		s.Group = GroupRustleBoost
	}

	out := SplitSRTP([]Server{srtp, plain, named})
	names := []string{}
	for _, s := range out {
		names = append(names, s.Name)
	}
	want := []string{"🇹🇷 Турция", "🇩🇪 Германия", "🇫🇮 Финляндия", "🇹🇷 Турция · Новый", "🇫🇮 Финляндия ⚡️"}
	if len(names) != len(want) {
		t.Fatalf("серверы: %q", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("серверы: %q, ждали %q", names, want)
		}
	}
	if HasSRTP(out[0]) || HasSRTP(out[1]) || HasSRTP(out[2]) {
		t.Error("у стабильных остался SRTP")
	}
	if !HasSRTP(out[3]) || !HasSRTP(out[4]) {
		t.Error("у новых нет SRTP")
	}
	if out[0].Params["relays"] != "9.9.9.9,8.8.8.8:3478" || out[3].Params["relays"] != "9.9.9.9,8.8.8.8:3478" {
		t.Errorf("релеи потерялись: %q / %q", out[0].Params["relays"], out[3].Params["relays"])
	}
	// Исходный сервер не тронут: параметры скопированы, а не общие.
	if !HasSRTP(srtp) {
		t.Error("исходный сервер потерял SRTP")
	}

	// Стабильный сохраняет прежний идентификатор, новый — свой.
	before := []Server{srtp}
	AssignStableIDs(before)
	AssignStableIDs(out)
	if out[0].ID != before[0].ID {
		t.Errorf("у стабильного сменился идентификатор: %s → %s", before[0].ID, out[0].ID)
	}
	if out[3].ID == out[0].ID {
		t.Error("у нового тот же идентификатор, что у стабильного")
	}
}
