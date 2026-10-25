package storage

import (
 "context"
 "encoding/json"
 "os"
 "testing"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/execution"
 "ai-trader/internal/tinvest"
)
func executionTestDB(t *testing.T)(*Store,context.Context,string){
 t.Helper();dsn:=os.Getenv("TEST_DATABASE_URL");if dsn==""{t.Skip("requires disposable PostgreSQL")}
 ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);t.Cleanup(cancel)
 db,err:=Open(ctx,dsn);if err!=nil{t.Fatal(err)};t.Cleanup(func(){db.Close()})
 if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)};id,err:=UUID();if err!=nil{t.Fatal(err)};account:="exec-test-"+id
 t.Cleanup(func(){cleanup,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();
  for _,query:=range []string{"DELETE FROM notification_outbox WHERE account_id=$1","DELETE FROM analysis_slots WHERE account_id=$1","DELETE FROM order_intents WHERE account_id=$1","DELETE FROM analysis_runs WHERE account_id=$1","DELETE FROM equity_samples WHERE account_id=$1","DELETE FROM funding_intents WHERE account_id=$1","DELETE FROM cashflows WHERE account_id=$1","DELETE FROM trading_accounts WHERE account_id=$1"}{if _,err:=db.db.ExecContext(cleanup,query,account);err!=nil{t.Error("test cleanup failed")}}
 })
 return db,ctx,account
}
func savedBuy(t *testing.T,db *Store,ctx context.Context,account string)string{
 t.Helper();id,err:=db.Begin(ctx,account,"codex_cli","test",analysis.PromptVersion);if err!=nil{t.Fatal(err)}
 s:=analysis.Snapshot{CapturedAt:time.Now().UTC()};if err:=db.Prepare(ctx,id,s,json.RawMessage(`{}`));err!=nil{t.Fatal(err)}
 result:=analysis.Result{Provider:"codex_cli",Decision:&analysis.Decision{Action:"BUY",Ticker:"SBER",ClassCode:"TQBR",Lots:2,Reason:"test"},Risk:&analysis.RiskReport{Allowed:true}}
 if err:=db.Finish(ctx,id,"validated_proposal",result,"");err!=nil{t.Fatal(err)};return id
}
func prepareOrder(t *testing.T,db *Store,ctx context.Context,account string)execution.Intent{
 t.Helper();run:=savedBuy(t,db,ctx,account);intent,err:=db.CreateIntent(ctx,account,run);if err!=nil{t.Fatal(err)}
 again,err:=db.CreateIntent(ctx,account,run);if err!=nil||again.ID!=intent.ID{t.Fatal("decision generated a second order UUID")}
 snapshot:=analysis.Snapshot{Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Ticker:"SBER",ClassCode:"TQBR",Lot:1,MinPriceIncrement:tinvest.Money{Nano:10000000}}}}}
 report:=analysis.RiskReport{Allowed:true,Pieces:2,EstimatedValueNano:"200400000000"}
 request,err:=execution.BuildLimit(intent,snapshot,report);if err!=nil{t.Fatal(err)}
 if err:=db.PrepareIntent(ctx,intent.ID,execution.Prepared{Request:request,Snapshot:snapshot,Risk:report});err!=nil{t.Fatal(err)}
 intent,err=db.GetIntent(ctx,intent.ID);if err!=nil{t.Fatal(err)};return intent
}
func TestSandboxExecutionPersistenceAndLedger(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 initial:=&analysis.AccountState{EquityNano:100000*amount.Scale,AvailableRUBNano:100000*amount.Scale}
 if err:=db.InitializeAccount(ctx,account,initial,time.Now().UTC().Add(-time.Minute));err!=nil{t.Fatal(err)}
 if err:=db.VerifyAccount(ctx,account,initial,true);err!=nil{t.Fatal(err)}
 intent:=prepareOrder(t,db,ctx,account)
 if err:=db.MarkSubmitting(ctx,intent.ID);err==nil{t.Fatal("paused account submitted an order")}
 if err:=db.Resume(ctx,account);err!=nil{t.Fatal(err)}
 if err:=db.MarkSubmitting(ctx,intent.ID);err!=nil{t.Fatal(err)}
 if err:=db.MarkSubmitting(ctx,intent.ID);err==nil{t.Fatal("order submitted twice")}
 if err:=db.SaveAcknowledgement(ctx,intent.ID,"exchange",json.RawMessage(`{"orderId":"exchange"}`));err!=nil{t.Fatal(err)}
 state:=tinvest.OrderState{ID:"exchange",LotsExecuted:"2",Raw:json.RawMessage(`{"orderId":"exchange","lotsExecuted":"2"}`)}
 if err:=db.SaveOrderState(ctx,intent.ID,"filled",state,-200100000000,100000000);err!=nil{t.Fatal(err)}
 actual:=&analysis.AccountState{EquityNano:99999900000000,AvailableRUBNano:99799900000000,Holdings:[]analysis.Holding{{UID:"uid",Pieces:2,FreePieces:2,ValueNano:200000000000}}}
 if err:=db.VerifyAccount(ctx,account,actual,true);err!=nil{t.Fatal(err)}
 if err:=db.SaveOrderState(ctx,intent.ID,"filled",state,-200100000000,100000000);err!=nil{t.Fatal(err)}
 if err:=db.VerifyAccount(ctx,account,actual,true);err!=nil{t.Fatal("same fill was counted twice:",err)}
 actual.AvailableRUBNano+=1000*amount.Scale;actual.EquityNano+=1000*amount.Scale
 if err:=db.VerifyAccount(ctx,account,actual,true);err==nil{t.Fatal("external money became unexplained profit")}
 flowID,_:=UUID();flow:=Cashflow{ID:flowID,AccountID:account,Amount:1000*amount.Scale,At:time.Now().UTC(),Source:"owner_record",Memo:"external test deposit"}
 if err:=db.RecordCashflow(ctx,flow);err!=nil{t.Fatal(err)};if err:=db.RecordCashflow(ctx,flow);err!=nil{t.Fatal(err)}
 if err:=db.VerifyAccount(ctx,account,actual,true);err!=nil{t.Fatal(err)}
 flow.Amount++;if err:=db.RecordCashflow(ctx,flow);err==nil{t.Fatal("conflicting flow was accepted")}
 events,err:=db.OrderEvents(ctx,account,intent.ID);if err!=nil{t.Fatal(err)};var history []json.RawMessage;if json.Unmarshal(events,&history)!=nil||len(history)<5{t.Fatal("order state audit is incomplete")}
}
func TestSubmissionGateBlocksConcurrentIntentAndFunding(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 state:=&analysis.AccountState{EquityNano:100000*amount.Scale,AvailableRUBNano:100000*amount.Scale}
 if err:=db.InitializeAccount(ctx,account,state,time.Now().UTC());err!=nil{t.Fatal(err)};if err:=db.Resume(ctx,account);err!=nil{t.Fatal(err)}
 first:=prepareOrder(t,db,ctx,account);second:=prepareOrder(t,db,ctx,account)
 if err:=db.MarkSubmitting(ctx,first.ID);err!=nil{t.Fatal(err)}
 if err:=db.MarkSubmitting(ctx,second.ID);err==nil{t.Fatal("second intent bypassed unresolved order")}
 fundingID,_:=UUID();if err:=db.BeginFunding(ctx,Funding{ID:fundingID,AccountID:account,Amount:1000000000,Before:state.AvailableRUBNano,At:time.Now().UTC()});err==nil{t.Fatal("funding raced an unresolved order")}
 if err:=db.MarkOrderError(ctx,first.ID,"unknown","response lost");err!=nil{t.Fatal(err)}
 if err:=db.Resume(ctx,account);err==nil{t.Fatal("resume ignored ambiguous order")}
}
func TestFundingJournalAndAdjustedEquity(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 when:=time.Date(2026,10,26,9,0,0,0,time.UTC)
 initial:=&analysis.AccountState{EquityNano:100000*amount.Scale,AvailableRUBNano:100000*amount.Scale}
 if err:=db.InitializeAccount(ctx,account,initial,when);err!=nil{t.Fatal(err)}
 if _,err:=db.RecordEquity(ctx,account,"",when,initial.EquityNano);err!=nil{t.Fatal(err)}
 id,_:=UUID();f:=Funding{ID:id,AccountID:account,Amount:50000*amount.Scale,Before:initial.AvailableRUBNano,At:when.Add(time.Minute)}
 if err:=db.BeginFunding(ctx,f);err!=nil{t.Fatal(err)}
 if err:=db.VerifyAccount(ctx,account,initial,true);err==nil{t.Fatal("unresolved funding was ignored")}
 if err:=db.FundingError(ctx,id,"response lost");err!=nil{t.Fatal(err)}
 if err:=db.ConfirmFunding(ctx,f);err!=nil{t.Fatal(err)}
 updated:=&analysis.AccountState{EquityNano:150000*amount.Scale,AvailableRUBNano:150000*amount.Scale}
 if err:=db.VerifyAccount(ctx,account,updated,true);err!=nil{t.Fatal(err)}
 m,err:=db.RecordEquity(ctx,account,"",when.Add(2*time.Minute),updated.EquityNano)
 if err!=nil||m.DailyChangeBPS!=0||m.DrawdownBPS!=0||m.AdjustedCurrentNano!="100000000000000"{t.Fatalf("deposit inflated performance: %+v %v",m,err)}
 if err:=db.ConfirmFunding(ctx,f);err==nil{t.Fatal("confirmed funding was counted a second time")}
}
func TestAccountSessionLock(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 release,err:=db.AcquireAccount(ctx,account);if err!=nil{t.Fatal(err)}
 if other,err:=db.AcquireAccount(ctx,account);err==nil{other();release();t.Fatal("account lock was not exclusive")}
 release();releaseAgain,err:=db.AcquireAccount(ctx,account);if err!=nil{t.Fatal(err)};releaseAgain()
}

func TestPendingFundingCannotBecomeTrackingAnchor(t *testing.T){
 db,ctx,account:=executionTestDB(t);id,_:=UUID()
 f:=Funding{ID:id,AccountID:account,Amount:1000*amount.Scale,Before:100000*amount.Scale,At:time.Now().UTC()}
 if err:=db.BeginFunding(ctx,f);err!=nil{t.Fatal(err)}
 state:=&analysis.AccountState{EquityNano:100000*amount.Scale,AvailableRUBNano:100000*amount.Scale}
 if err:=db.InitializeAccount(ctx,account,state,time.Now().UTC());err==nil{t.Fatal("anchor captured during ambiguous funding")}
 control,err:=db.TradingControl(ctx,account);if err!=nil||control.Initialized{t.Fatal("failed initialization changed anchor")}
}
