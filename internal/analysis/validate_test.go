package analysis

import (
	"testing"
	"time"

	"ai-trader/internal/tinvest"
)

func TestDecodeDecision(t *testing.T) {
	valid := `{"action":"HOLD","ticker":"","class_code":"","lots":0,"reason":"Недостаточно данных"}`
	if _, err := DecodeDecision([]byte(valid)); err != nil { t.Fatal(err) }
	for _, input := range []string{
		`{}`, `null`, `{"action":null}`, valid + `{}`,
		`{"action":"BUY","ticker":"SBER","class_code":"TQBR","lots":0.5,"reason":"test"}`,
		`{"action":"HOLD","ticker":"","class_code":"","lots":0,"reason":"test","override_risk":true}`,
	} {
		if _, err := DecodeDecision([]byte(input)); err == nil { t.Errorf("accepted invalid response: %s", input) }
	}
}

func TestValidateMembershipAndFreshness(t *testing.T) {
	now := time.Now().UTC()
	price := tinvest.Money{Units: "100", Currency: "rub"}
	quoteTime := now.Add(-time.Minute)
	snapshot := Snapshot{CapturedAt: now, Mode: "protective", Preferred: []string{"SBER:TQBR"}, Quotes: []Quote{
		{Instrument: tinvest.Instrument{Ticker: "SBER", ClassCode: "TQBR", Kind: "share", Currency: "rub"}, Price: &price, Time: &quoteTime, Usable: true},
		{Instrument: tinvest.Instrument{Ticker: "LKOH", ClassCode: "TQBR", Kind: "share", Currency: "rub"}, Price: &price, Time: &quoteTime, Usable: true},
	}}
	buy := Decision{Action: "BUY", Ticker: "SBER", ClassCode: "TQBR", Lots: 1, Reason: "Тест предложения"}
	if err := Validate(buy, snapshot, now, 30*time.Minute); err != nil { t.Fatal(err) }
	outside := buy; outside.Ticker = "LKOH"
	if err := Validate(outside, snapshot, now, 30*time.Minute); err == nil { t.Fatal("protective mode admitted outside instrument") }
	snapshot.Mode = "experimental"
	if err := Validate(outside, snapshot, now, 30*time.Minute); err != nil { t.Fatal(err) }
	outside.Ticker = "UNKNOWN"
	if err := Validate(outside, snapshot, now, 30*time.Minute); err == nil { t.Fatal("admitted instrument without context") }
	if err := Validate(buy, snapshot, now.Add(time.Hour), 30*time.Minute); err == nil { t.Fatal("accepted stale snapshot") }
	snapshot.Quotes[0].Usable = false
	if err := Validate(buy, snapshot, now, 30*time.Minute); err == nil { t.Fatal("accepted unusable quote") }
	hold := Decision{Action: "HOLD", Reason: "Нет свежих цен"}
	if err := Validate(hold, snapshot, now, 30*time.Minute); err != nil { t.Fatal(err) }
	hold.Lots = 1
	if err := Validate(hold, snapshot, now, 30*time.Minute); err == nil { t.Fatal("accepted nonzero HOLD") }
	buy.Lots = -1
	if err := Validate(buy, snapshot, now, 30*time.Minute); err == nil { t.Fatal("accepted negative lots") }
}
