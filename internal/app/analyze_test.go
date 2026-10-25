package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"ai-trader/internal/analysis"
	"ai-trader/internal/config"
	"ai-trader/internal/tinvest"
 "ai-trader/internal/storage"
)

type fakeBroker struct { stale bool; fail bool }
func (f fakeBroker) Accounts(context.Context) (tinvest.Accounts, error) {
	if f.fail { return tinvest.Accounts{}, fmt.Errorf("broker unavailable") }
	return tinvest.Accounts{Accounts: []tinvest.Account{{ID: "sandbox", Status: "ACCOUNT_STATUS_OPEN"}}}, nil
}
func (f fakeBroker) Snapshot(_ context.Context, _ string, portfolio bool) (json.RawMessage, error) {
	if portfolio { return json.RawMessage(`{"accountId":"sandbox","totalAmountPortfolio":{"units":"100000","nano":0,"currency":"rub"},"positions":[{"instrumentType":"currency","ticker":"RUB000UTSTOM","quantity":{"units":"100000","nano":0},"currentPrice":{"units":"1","nano":0,"currency":"rub"}}]}`), nil }
	return json.RawMessage(`{"accountId":"sandbox","money":[{"units":"100000","currency":"rub","nano":0}],"securities":[]}`), nil
}
func (f fakeBroker) Instrument(_ context.Context, ticker, class string) (tinvest.Instrument, error) {
	return tinvest.Instrument{Exchange:"MOEX", UID: "uid", Ticker: ticker, ClassCode: class, Lot: 10, Kind: "share", Currency: "rub"}, nil
}
func (f fakeBroker) Prices(context.Context, []string) (tinvest.Prices, error) {
	when := time.Now().Add(-time.Minute)
	if f.stale { when = time.Now().Add(-time.Hour) }
	return tinvest.Prices{Prices: []tinvest.LastPrice{{UID: "uid", Price: tinvest.Money{Units: "100", Currency: "rub"}, Time: when}}}, nil
}

type fakeStore struct {
	prepared bool
	prepareFail bool
	status string
	message string
	request json.RawMessage
	result analysis.Result
 paused bool
 reservation *analysis.Reservation
 reserved int
 ledgerFail bool
 snapshot analysis.Snapshot
}
func (f *fakeStore) Begin(context.Context, string, string, string, string) (string, error) { return "run-id", nil }
func (f *fakeStore) Prepare(_ context.Context, _ string, snapshot analysis.Snapshot, request json.RawMessage) error {
	if f.prepareFail { return fmt.Errorf("database unavailable") }
	f.snapshot=snapshot
 f.prepared = true
	f.request = append(json.RawMessage{}, request...)
	return nil
}
func (f *fakeStore) Finish(ctx context.Context, _ string, status string, result analysis.Result, message string) error {
	if ctx.Err() != nil { return fmt.Errorf("audit context was cancelled") }
	f.status, f.result, f.message = status, result, message
	return nil
}

type fakeModel struct {
	store *fakeStore
	calls int
	decision analysis.Decision
	fail bool
	cancel context.CancelFunc
}
func (f *fakeModel) Respond(_ context.Context, request json.RawMessage) (analysis.Result, error) {
	f.calls++
	if !f.store.prepared || string(f.store.request) != string(request) { return analysis.Result{}, fmt.Errorf("model called before durable request") }
	if f.cancel != nil { f.cancel() }
	if f.fail { return analysis.Result{Usage: &analysis.Usage{TotalTokens: 5}}, fmt.Errorf("model failed") }
	return analysis.Result{Decision: &f.decision, Usage: &analysis.Usage{TotalTokens: 12}}, nil
}

func TestAnalyzeLifecycle(t *testing.T) {
	cases := []struct { name string; stale, brokerFail, prepareFail, modelFail bool; action, expected string; modelCalls int }{
		{"hold", false, false, false, false, "HOLD", "validated_proposal", 1},
		{"fresh buy", false, false, false, false, "BUY", "validated_proposal", 1},
		{"stale buy", true, false, false, false, "BUY", "skipped", 0},
		{"stale hold", true, false, false, false, "HOLD", "skipped", 0},
		{"broker error", false, true, false, false, "HOLD", "broker_error", 0},
		{"database failure before model", false, false, true, false, "HOLD", "", 0},
		{"model error", false, false, false, true, "HOLD", "model_error", 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			db := &fakeStore{prepareFail: tt.prepareFail}
			d := analysis.Decision{Action: tt.action, Reason: "Тест предложения"}
			if tt.action == "BUY" { d.Ticker, d.ClassCode, d.Lots = "SBER", "TQBR", 1 }
			model := &fakeModel{store: db, decision: d, fail: tt.modelFail}
			c := config.Config{AccountID: "sandbox", Mode: "protective", Preferred: []string{"SBER:TQBR"}, MarketMaxAge: time.Minute, SlippageBPS:20, QuoteMaxAge: 30*time.Minute}
			err := analyze(context.Background(), c, config.Analysis{Model: "test", MaxTokens: 2048}, []string{"SBER:TQBR"}, fakeBroker{stale: tt.stale, fail: tt.brokerFail}, db, model, io.Discard)
			if db.status != tt.expected || model.calls != tt.modelCalls { t.Fatalf("status=%s calls=%d error=%v", db.status, model.calls, err) }
			if (err == nil) != (tt.expected == "validated_proposal" || tt.expected == "skipped") { t.Fatalf("unexpected error: %v", err) }
			if strings.Contains(string(db.request), "accountId") || strings.Contains(string(db.request), "sandbox") { t.Fatal("account identifier leaked into model request") }
			if tt.modelFail && (db.result.Usage == nil || db.result.Usage.TotalTokens != 5) { t.Fatal("failed response usage not preserved") }
		})
	}
}

func TestCancellationStillRecordsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()); defer cancel()
	db := &fakeStore{}
	model := &fakeModel{store: db, fail: true, cancel: cancel}
	c := config.Config{AccountID: "sandbox", Mode: "experimental", MarketMaxAge: time.Minute, SlippageBPS:20, QuoteMaxAge: 30*time.Minute}
	err := analyze(ctx, c, config.Analysis{Model: "test", MaxTokens: 2048}, []string{"LKOH:TQBR"}, fakeBroker{}, db, model, io.Discard)
	if err == nil || db.status != "model_error" { t.Fatalf("failure not persisted: %s %v", db.status, err) }
}

func TestCandidateModes(t *testing.T) {
	c := config.Config{Mode: "protective", Preferred: []string{"SBER:TQBR"}}
	if _, err := candidateKeys(c, "LKOH:TQBR"); err == nil { t.Fatal("protective mode admitted outside instrument") }
	c.Mode = "experimental"
	keys, err := candidateKeys(c, "lkoh:tqbr,LKOH:TQBR")
	if err != nil || len(keys) != 1 || keys[0] != "LKOH:TQBR" { t.Fatalf("%v %v", keys, err) }
	for _, input := range []string{"", "LKOH", ":TQBR", "LKOH:"} {
		if _, err := candidateKeys(c, input); err == nil { t.Fatalf("accepted %q", input) }
	}
}


func TestCodexAnalysisUsesSharedValidation(t *testing.T) {
 db := &fakeStore{}
 model := &fakeModel{store: db, decision: analysis.Decision{Action: "HOLD", Reason: "Мало данных"}}
 c := config.Config{AccountID: "sandbox", Mode: "experimental", MarketMaxAge: time.Minute, SlippageBPS:20, QuoteMaxAge: 30*time.Minute}
 err := analyze(context.Background(), c, config.Analysis{Provider: "codex_cli", Model: "codex-default"}, []string{"LKOH:TQBR"}, fakeBroker{}, db, model, io.Discard)
 if err != nil || db.status != "validated_proposal" { t.Fatalf("%s %v", db.status, err) }
 if db.result.Provider != "codex_cli" || !strings.Contains(string(db.request), "output_schema") { t.Fatal("wrong provider in history") }
}


func (f fakeBroker) OpenOrderCount(context.Context,string)(int,error){return 0,nil}
func (f fakeBroker) Candles(context.Context,string,time.Time,time.Time)(tinvest.Candles,error){
 p:=tinvest.Money{Units:"100"};return tinvest.Candles{Candles:[]tinvest.Candle{{Time:time.Now().UTC().Add(-2*time.Hour).Truncate(time.Hour),Open:p,High:p,Low:p,Close:p,Volume:"100",Complete:true}}},nil
}
func (f fakeBroker) TradingStatus(context.Context,string)(tinvest.TradingStatus,error){return tinvest.TradingStatus{UID:"uid",Status:"SECURITY_TRADING_STATUS_NORMAL_TRADING",APIAvailable:true,MarketAvailable:true,LimitAvailable:true},nil}
func (f fakeBroker) OrderBook(context.Context,string)(tinvest.OrderBook,error){return tinvest.OrderBook{UID:"uid",Currency:"rub",Time:time.Now().UTC(),Bids:[]tinvest.BookLevel{{Price:tinvest.Money{Units:"99"},Quantity:"100"}},Asks:[]tinvest.BookLevel{{Price:tinvest.Money{Units:"100"},Quantity:"100"}}},nil}
func (f fakeBroker) Schedules(context.Context,string,time.Time,time.Time)(tinvest.Schedules,error){now:=time.Now().UTC();return tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Trading:true,Start:now.Add(-time.Hour),End:now.Add(time.Hour)}}}}},nil}
func (f *fakeStore) RecordEquity(_ context.Context,_ string,_ string,when time.Time,current int64)(analysis.EquityMetrics,error){return analysis.EquityMetrics{ObservedAt:when,CurrentNano:"100000000000000",DayStartNano:"100000000000000",HighWaterNano:"100000000000000"},nil}

func(f *fakeStore)VerifyAccount(context.Context,string,*analysis.AccountState,bool)error{if f.ledgerFail{return fmt.Errorf("unrecorded cash flow")};return nil}
func(f *fakeStore)TradingControl(context.Context,string)(storage.TradingControl,error){return storage.TradingControl{Paused:f.paused,Initialized:true},nil}
func(f *fakeStore)ReserveModel(context.Context,string,string,bool,int,time.Duration)(analysis.Reservation,error){f.reserved++;if f.reservation!=nil{return *f.reservation,nil};return analysis.Reservation{Attempt:true},nil}

func TestEconomyGatesAndReuse(t *testing.T){
 for _,tt:=range []struct{name string;paused,ledger bool;reservation *analysis.Reservation;status string;calls int}{
  {name:"paused",paused:true,status:"skipped"},
  {name:"ledger mismatch",ledger:true,status:"skipped"},
  {name:"daily quota",reservation:&analysis.Reservation{Reason:"daily budget"},status:"skipped"},
  {name:"failed duplicate",reservation:&analysis.Reservation{SourceID:"previous",SourceStatus:"model_error",Reason:"already attempted"},status:"skipped"},
  {name:"reuse",reservation:&analysis.Reservation{SourceID:"previous",SourceStatus:"validated_proposal",SourceResult:analysis.Result{Decision:&analysis.Decision{Action:"HOLD",Reason:"Нет сигнала"},Usage:&analysis.Usage{TotalTokens:123}}},status:"reused"},
 }{
  t.Run(tt.name,func(t *testing.T){
   db:=&fakeStore{paused:tt.paused,ledgerFail:tt.ledger,reservation:tt.reservation}
   model:=&fakeModel{store:db,decision:analysis.Decision{Action:"HOLD",Reason:"Тест"}}
   c:=config.Config{AccountID:"sandbox",Mode:"protective",Preferred:[]string{"SBER:TQBR"},MarketMaxAge:time.Minute,QuoteMaxAge:30*time.Minute,SlippageBPS:20}
   err:=analyze(context.Background(),c,config.Analysis{Provider:"codex_cli"},[]string{"SBER:TQBR"},fakeBroker{},db,model,io.Discard)
   if err!=nil||db.status!=tt.status||model.calls!=tt.calls{t.Fatalf("status=%s calls=%d error=%v",db.status,model.calls,err)}
   if db.result.Usage!=nil||db.result.Economy==nil||db.result.Economy.ModelAttempted{t.Fatal("charged usage to skipped/reused request")}
   if !db.prepared||len(db.snapshot.Quotes)!=1{t.Fatal("lost full audit snapshot")}
   if (tt.paused||tt.ledger)&&db.reserved!=0{t.Fatal("spent model reservation before data gate")}
  })
 }
}

func TestOversizedContextNeverCallsModel(t *testing.T){
 db:=&fakeStore{};model:=&fakeModel{store:db}
 c:=config.Config{AccountID:"sandbox",Mode:"experimental",MarketMaxAge:time.Minute,QuoteMaxAge:30*time.Minute,SlippageBPS:20}
 err:=analyze(context.Background(),c,config.Analysis{Provider:"codex_cli",MaxInputBytes:10},[]string{"SBER:TQBR"},fakeBroker{},db,model,io.Discard)
 if err!=nil||db.status!="skipped"||model.calls!=0||db.reserved!=0{t.Fatalf("oversized: %s %v",db.status,err)}
}

type closedBroker struct{fakeBroker}
func(closedBroker)Schedules(context.Context,string,time.Time,time.Time)(tinvest.Schedules,error){return tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Trading:false}}}}},nil}
func TestClosedMarketSkipsWithoutCallingModel(t *testing.T){
 db:=&fakeStore{};model:=&fakeModel{store:db}
 c:=config.Config{AccountID:"sandbox",Mode:"experimental",MarketMaxAge:time.Minute,QuoteMaxAge:30*time.Minute,SlippageBPS:20}
 err:=analyze(context.Background(),c,config.Analysis{Provider:"codex_cli",Refresh:true},[]string{"SBER:TQBR"},closedBroker{},db,model,io.Discard)
 if err!=nil||db.status!="skipped"||model.calls!=0||db.reserved!=0{t.Fatalf("closed market (even with refresh): %s %v",db.status,err)}
}

func TestBuyHaltDoesNotFilterReducingSell(t *testing.T){
 c:=config.Config{AccountID:"sandbox",Mode:"experimental",MarketMaxAge:time.Minute,QuoteMaxAge:30*time.Minute,SlippageBPS:20}
 snapshot,err:=collectSnapshot(context.Background(),c,[]string{"SBER:TQBR"},fakeBroker{});if err!=nil{t.Fatal(err)}
 metrics,_:=(&fakeStore{}).RecordEquity(context.Background(),"","",snapshot.CapturedAt,snapshot.Account.EquityNano)
 metrics.DailyHalt=true;snapshot.Equity=&metrics
 snapshot.Account.Holdings=[]analysis.Holding{{UID:"uid",Ticker:"SBER",Pieces:10,FreePieces:10,ValueNano:1000000000000}}
 filtered,_:=eligibleContext(snapshot,c,time.Now().UTC());if len(filtered.Quotes)!=1{t.Fatal("loss halt filtered a permissible reducing SELL")}
 snapshot.Account.Holdings=nil
 filtered,_=eligibleContext(snapshot,c,time.Now().UTC());if len(filtered.Quotes)!=0{t.Fatal("loss halt admitted a BUY without holdings to reduce")}
}
func TestLongModelAnswerRejectedWithoutRetryAndUsagePreserved(t *testing.T){
 db:=&fakeStore{};model:=&fakeModel{store:db,decision:analysis.Decision{Action:"HOLD",Reason:strings.Repeat("а",601)}}
 c:=config.Config{AccountID:"sandbox",Mode:"experimental",MarketMaxAge:time.Minute,QuoteMaxAge:30*time.Minute,SlippageBPS:20}
 err:=analyze(context.Background(),c,config.Analysis{Provider:"codex_cli"},[]string{"SBER:TQBR"},fakeBroker{},db,model,io.Discard)
 if err==nil||db.status!="model_error"||model.calls!=1||db.reserved!=1||db.result.Usage==nil{t.Fatalf("status=%s calls=%d error=%v",db.status,model.calls,err)}
}
