package app

import (
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "io"
 "net/url"
 "os"
 "strconv"
 "strings"
 "testing"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/config"
 "ai-trader/internal/schedule"
 "ai-trader/internal/storage"
 "ai-trader/internal/tinvest"
)

// Stateful broker double: real PostgreSQL and the production scheduler,
// analysis, risk, executor and ledger are exercised without network requests.
type automaticBroker struct{
 fakeBroker
 account string
 cash,shares int64
 posts int
 reads,queries int
 lostResponse bool
 states map[string]tinvest.OrderState
}
func testMoney(n int64)tinvest.Money{return tinvest.Money{Currency:"rub",Units:strconv.FormatInt(n/amount.Scale,10),Nano:int32(n%amount.Scale)}}
func(b *automaticBroker)Accounts(context.Context)(tinvest.Accounts,error){b.reads++;return tinvest.Accounts{Accounts:[]tinvest.Account{{ID:b.account,Status:"ACCOUNT_STATUS_OPEN"}}},nil}
func(b *automaticBroker)Instrument(ctx context.Context,ticker,class string)(tinvest.Instrument,error){i,e:=b.fakeBroker.Instrument(ctx,ticker,class);i.MinPriceIncrement=tinvest.Money{Nano:10000000};return i,e}
func(b *automaticBroker)Snapshot(_ context.Context,_ string,portfolio bool)(json.RawMessage,error){
 if !portfolio{
  securities:=[]any{};if b.shares>0{securities=append(securities,map[string]any{"instrumentUid":"uid","balance":strconv.FormatInt(b.shares,10),"blocked":"0"})}
  return json.Marshal(map[string]any{"money":[]tinvest.Money{testMoney(b.cash)},"securities":securities})
 }
 positions:=[]any{map[string]any{"instrumentType":"currency","ticker":"RUB000UTSTOM","quantity":testMoney(b.cash),"currentPrice":testMoney(amount.Scale)}}
 if b.shares>0{positions=append(positions,map[string]any{"instrumentType":"share","instrumentUid":"uid","ticker":"SBER","quantity":testMoney(b.shares*amount.Scale),"currentPrice":testMoney(100*amount.Scale)})}
 return json.Marshal(map[string]any{"totalAmountPortfolio":testMoney(b.cash+b.shares*100*amount.Scale),"positions":positions})
}
func(b *automaticBroker)Schedules(context.Context,string,time.Time,time.Time)(tinvest.Schedules,error){
 now:=time.Now().UTC();return tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Trading:true,Start:now.Add(-65*time.Minute),End:now.Add(5*time.Hour)}}}}},nil
}
func(b *automaticBroker)PostLimit(_ context.Context,r tinvest.LimitRequest)(tinvest.PostOrderResult,error){
 b.posts++
 if _,exists:=b.states[r.OrderID];exists{return tinvest.PostOrderResult{},fmt.Errorf("duplicate POST")}
 lots,_:=strconv.ParseInt(r.Quantity,10,64);pieces:=lots*10;price:=int64(100)*amount.Scale
 if r.Direction=="ORDER_DIRECTION_SELL"{price=99*amount.Scale}
 gross:=pieces*price;fee:=gross*5/10000
 if r.Direction=="ORDER_DIRECTION_BUY"{b.cash-=gross+fee;b.shares+=pieces}else{b.cash+=gross-fee;b.shares-=pieces}
 state:=tinvest.OrderState{ID:r.OrderID,UID:"uid",Direction:r.Direction,OrderType:r.OrderType,Status:"EXECUTION_REPORT_STATUS_FILL",LotsRequested:r.Quantity,LotsExecuted:r.Quantity,Total:testMoney(gross),Commission:testMoney(fee)}
 state.Raw,_=json.Marshal(state);b.states[r.OrderID]=state
 if b.lostResponse{return tinvest.PostOrderResult{},fmt.Errorf("simulated lost response after execution")}
 return tinvest.PostOrderResult{ID:r.OrderID,Raw:state.Raw},nil
}
func(b *automaticBroker)OrderState(_ context.Context,_,id string,_ bool)(tinvest.OrderState,error){b.queries++;s,ok:=b.states[id];if !ok{return s,fmt.Errorf("not found")};return s,nil}
func(b *automaticBroker)CancelSandbox(context.Context,string,string)error{return fmt.Errorf("unexpected cancellation")}
type automaticModel struct{action string;calls int;after func()}
func(m *automaticModel)Respond(context.Context,json.RawMessage)(analysis.Result,error){
 m.calls++;d:=&analysis.Decision{Action:m.action,Reason:"Deterministic integration test"}
 if m.action!="HOLD"{d.Ticker="SBER";d.ClassCode="TQBR";d.Lots=1}
 if m.after!=nil{m.after()}
 return analysis.Result{Decision:d},nil
}
func automaticFixture(t *testing.T)(context.Context,*storage.Store,config.Config,config.Analysis,*automaticBroker){
 t.Helper();dsn:=os.Getenv("TEST_DATABASE_URL");if dsn==""{t.Skip("requires disposable PostgreSQL")}
 ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);t.Cleanup(cancel)
 // Separate schema from storage-package tests, which run migrations too.
 // Concurrent schema-changing migrations must not run against an active ledger.
 id,_:=storage.UUID();schema:="auto_test_"+strings.ReplaceAll(id,"-","")
 admin,err:=sql.Open("postgres",dsn);if err!=nil{t.Fatal(err)}
 if _,err:=admin.ExecContext(ctx,"CREATE SCHEMA "+schema);err!=nil{admin.Close();t.Fatal(err)}
 u,err:=url.Parse(dsn);if err!=nil{t.Fatal(err)};q:=u.Query();q.Set("search_path",schema);u.RawQuery=q.Encode()
 db,err:=storage.Open(ctx,u.String());if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){db.Close();if _,err:=admin.Exec("DROP SCHEMA "+schema+" CASCADE");err!=nil{t.Error(err)};admin.Close()})
 if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)}
 account:="auto-test-"+id
 b:=&automaticBroker{account:account,cash:100000*amount.Scale,states:map[string]tinvest.OrderState{}}
 state:=&analysis.AccountState{EquityNano:b.cash,AvailableRUBNano:b.cash}
 if err:=db.InitializeAccount(ctx,account,state,time.Now().UTC());err!=nil{t.Fatal(err)};if err:=db.Resume(ctx,account);err!=nil{t.Fatal(err)}
 c:=config.Config{Token:"fake-test-token",AccountID:account,Mode:"protective",Preferred:[]string{"SBER:TQBR"},MarketMaxAge:time.Minute,QuoteMaxAge:30*time.Minute,SlippageBPS:20,CandleLimit:24}
 settings:=config.Analysis{Provider:"codex_cli",Model:"test",AutoExecute:true,DailyLimit:2,CacheTTL:time.Minute}
 return ctx,db,c,settings,b
}
func TestAutomaticBuySellAndRestartDoNotDuplicateOrders(t *testing.T){
 ctx,db,c,s,b:=automaticFixture(t);m:=&automaticModel{action:"BUY"}
 if err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard);err!=nil{t.Fatal(err)}
 if b.posts!=1||b.shares!=10{t.Fatalf("BUY was not automatic: posts=%d shares=%d",b.posts,b.shares)}
 // A new process/tick in the same persisted window must not call the model again.
 if err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard);err!=nil{t.Fatal(err)}
 if m.calls!=1||b.posts!=1{t.Fatal("window duplicated analysis or order")}
 m.action="SELL"
 if err:=automationTickNow(ctx,c,s,c.Preferred,db,b,m,io.Discard,true);err!=nil{t.Fatal(err)}
 if b.posts!=2||b.shares!=0{t.Fatalf("SELL was not automatic: posts=%d shares=%d",b.posts,b.shares)}
 if err:=automationTickNow(ctx,c,s,c.Preferred,db,b,m,io.Discard,true);err!=nil{t.Fatal(err)}
 if b.posts!=2||m.calls!=2{t.Fatal("daily limit allowed another trade/model call")}
 if err:=verifyAfterExecution(ctx,c,db,b);err!=nil{t.Fatal(err)}
}
func TestAutomaticLostResponseRecoversWhilePausedWithoutReposting(t *testing.T){
 ctx,db,c,s,b:=automaticFixture(t);b.lostResponse=true;m:=&automaticModel{action:"BUY"}
 if err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard);err==nil{t.Fatal("lost response was ignored")}
 control,_:=db.TradingControl(ctx,c.AccountID);if !control.Paused{t.Fatal("unknown execution did not pause")}
 for i:=0;i<2;i++{if err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard);err!=nil{t.Fatal(err)}}
 if b.posts!=1||b.shares!=10||m.calls!=1{t.Fatal("recovery resubmitted or called model")}
 items,err:=db.ListIntents(ctx,c.AccountID,false);if err!=nil||len(items)!=1||items[0].State!="filled"{t.Fatal("recovery did not record actual fill",err)}
}
func TestAutomaticRecoversSavedAndPreparedProposals(t *testing.T){
 for _,prepared:=range []bool{false,true}{t.Run(fmt.Sprint(prepared),func(t *testing.T){
  ctx,db,c,s,b:=automaticFixture(t);s.Automatic=true;m:=&automaticModel{action:"BUY"}
  if err:=analyze(ctx,c,s,c.Preferred,b,db,m,io.Discard);err!=nil{t.Fatal(err)}
  ids,err:=db.AutomaticCandidates(ctx,c.AccountID);if err!=nil||len(ids)!=1{t.Fatal("missing saved work",err)}
  if prepared{if _,err:=db.CreateIntent(ctx,c.AccountID,ids[0]);err!=nil{t.Fatal(err)}}
  if err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard);err!=nil{t.Fatal(err)}
  if b.posts!=1||m.calls!=1{t.Fatal("restart lost or duplicated saved work")}
 })}
}
func TestAutomaticDisabledHoldAndEmergencyPauseNeverSubmit(t *testing.T){
 for _,mode:=range []string{"disabled","hold","pause"}{t.Run(mode,func(t *testing.T){
  ctx,db,c,s,b:=automaticFixture(t);m:=&automaticModel{action:"BUY"}
  switch mode{case "disabled":s.AutoExecute=false;case "hold":m.action="HOLD";case "pause":m.after=func(){if err:=db.Pause(ctx,c.AccountID,"test emergency pause");err!=nil{t.Fatal(err)}}}
  err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard)
  if mode!="pause"&&err!=nil{t.Fatal(err)}
  if b.posts!=0{t.Fatal("unexpected automatic POST")}
 })}
}

func TestOutsideWorkingHoursBlocksEvenImmediateRequestsAndRecovery(t *testing.T){
 ctx,db,c,s,b:=automaticFixture(t);m:=&automaticModel{action:"BUY"}
 // Persist an uncertain submitted order first, so recovery is also tested.
 b.lostResponse=true
 if err:=automationTick(ctx,c,s,c.Preferred,db,b,m,io.Discard);err==nil{t.Fatal("expected lost response")}
 local:=time.Now().In(time.FixedZone("Europe/Moscow",3*3600));minute:=local.Hour()*60+local.Minute()
 s.Hours=schedule.Hours{Enabled:true,Start:minute+1,End:minute+2}
 reads,queries,posts,calls:=b.reads,b.queries,b.posts,m.calls
 for _,immediate:=range []bool{false,true}{
  if err:=automationTickNow(ctx,c,s,c.Preferred,db,b,m,io.Discard,immediate);err!=nil{t.Fatal(err)}
 }
 if b.reads!=reads||b.queries!=queries||b.posts!=posts||m.calls!=calls{t.Fatal("out-of-hours tick made broker/model requests")}
}
