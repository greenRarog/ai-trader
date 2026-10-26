package execution

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "strconv"
 "testing"
 "time"

 "ai-trader/internal/analysis"
 "ai-trader/internal/tinvest"
)

type memoryStore struct{intent Intent;prepareCalls,submitCalls int;failPrepare,failAck,gateClosed bool}
func(s *memoryStore)GetIntent(context.Context,string)(Intent,error){return s.intent,nil}
func(s *memoryStore)PrepareIntent(_ context.Context,_ string,p Prepared)error{if s.failPrepare{return fmt.Errorf("db failed")};s.prepareCalls++;s.intent.Request=&p.Request;s.intent.Snapshot=&p.Snapshot;s.intent.Risk=&p.Risk;return nil}
func(s *memoryStore)MarkSubmitting(context.Context,string)error{if s.gateClosed||s.intent.State!="prepared"||s.intent.Request==nil{return fmt.Errorf("gate closed")};s.submitCalls++;s.intent.State="submitting";return nil}
func(s *memoryStore)SaveAcknowledgement(_ context.Context,_,brokerID string,_ json.RawMessage)error{if s.failAck{return fmt.Errorf("db failed after post")};s.intent.BrokerID=brokerID;s.intent.State="acknowledged";return nil}
func(s *memoryStore)SaveOrderObservation(context.Context,string,json.RawMessage)error{return nil}
func(s *memoryStore)SaveOrderState(_ context.Context,_,status string,state tinvest.OrderState,cash,fee int64)error{s.intent.State=status;s.intent.BrokerID=state.ID;s.intent.ExecutedLots,_=strconv.ParseInt(state.LotsExecuted,10,64);s.intent.CashDeltaNano=cash;s.intent.CommissionNano=fee;return nil}
func(s *memoryStore)MarkOrderError(_ context.Context,_,state,message string)error{s.intent.State=state;s.intent.Error=message;return nil}

type fakeExecutionBroker struct{posts,queries,cancels int;postError,stateError error;state tinvest.OrderState;lookupRequestID bool;requestUUID string}
func(b *fakeExecutionBroker)PostLimit(_ context.Context,r tinvest.LimitRequest)(tinvest.PostOrderResult,error){b.posts++;b.requestUUID=r.OrderID;if b.postError!=nil{return tinvest.PostOrderResult{},b.postError};return tinvest.PostOrderResult{ID:"exchange",Raw:json.RawMessage(`{"orderId":"exchange"}`)},nil}
func(b *fakeExecutionBroker)OrderState(_ context.Context,_,_ string,requestID bool)(tinvest.OrderState,error){b.queries++;b.lookupRequestID=requestID;return b.state,b.stateError}
func(b *fakeExecutionBroker)CancelSandbox(context.Context,string,string)error{b.cancels++;b.state.Status="EXECUTION_REPORT_STATUS_CANCELLED";return nil}
func executionFixture()(*memoryStore,*fakeExecutionBroker,Preflight){
 i:=Intent{ID:"11111111-1111-4111-8111-111111111111",AccountID:"sandbox",AnalysisID:"analysis",State:"prepared",CreatedAt:time.Now(),AnalysisCreatedAt:time.Now(),Decision:analysis.Decision{Action:"BUY",Ticker:"SBER",ClassCode:"TQBR",Lots:2,Reason:"test"}}
 snapshot:=analysis.Snapshot{Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Ticker:"SBER",ClassCode:"TQBR",Lot:1,MinPriceIncrement:tinvest.Money{Nano:10000000}}}}}
 report:=analysis.RiskReport{Allowed:true,Pieces:2,EstimatedValueNano:"200400000000"}
 request,_:=BuildLimit(i,snapshot,report)
 preflight:=func(context.Context,Intent)(Prepared,error){return Prepared{Request:request,Snapshot:snapshot,Risk:report},nil}
 state:=tinvest.OrderState{ID:"exchange",UID:"uid",Direction:"ORDER_DIRECTION_BUY",OrderType:"ORDER_TYPE_LIMIT",Status:"EXECUTION_REPORT_STATUS_FILL",LotsRequested:"2",LotsExecuted:"2",Total:tinvest.Money{Currency:"rub",Units:"200"},Commission:tinvest.Money{Currency:"rub",Units:"0",Nano:100000000},Raw:json.RawMessage(`{}`)}
 return &memoryStore{intent:i},&fakeExecutionBroker{state:state},preflight
}
func TestSubmitOnceAndRecordActualFill(t *testing.T){
 db,broker,preflight:=executionFixture();ctx:=context.Background()
 result,err:=Perform(ctx,db,broker,db.intent.ID,preflight);if err!=nil{t.Fatal(err)}
 if result.State!="filled"||result.CashDeltaNano!=-200100000000||result.CommissionNano!=100000000||broker.posts!=1||db.submitCalls!=1{t.Fatalf("wrong fill: %+v posts=%d",result,broker.posts)}
 if _,err=Perform(ctx,db,broker,db.intent.ID,preflight);err!=nil||broker.posts!=1{t.Fatal("terminal intent was submitted twice")}
}
func TestTimeoutRecoversByOriginalRequestID(t *testing.T){
 db,broker,preflight:=executionFixture();ctx:=context.Background();broker.postError=errors.New("response lost")
 if _,err:=Perform(ctx,db,broker,db.intent.ID,preflight);err==nil||db.intent.State!="unknown"{t.Fatal("ambiguous submission not persisted")}
 broker.postError=nil
 result,err:=Perform(ctx,db,broker,db.intent.ID,func(context.Context,Intent)(Prepared,error){t.Fatal("must not preflight/repost unknown intent");return Prepared{},nil})
 if err!=nil||result.State!="filled"||broker.posts!=1||!broker.lookupRequestID||broker.requestUUID!=db.intent.ID{t.Fatalf("recovery failed: %+v %v",result,err)}
}
func TestLostAcknowledgementNeverReposts(t *testing.T){
 db,broker,preflight:=executionFixture();db.failAck=true
 if _,err:=Perform(context.Background(),db,broker,db.intent.ID,preflight);err==nil||db.intent.State!="submitting"{t.Fatal("expected durable submitting state")}
 db.failAck=false
 if result,err:=Perform(context.Background(),db,broker,db.intent.ID,preflight);err!=nil||result.State!="filled"||broker.posts!=1||!broker.lookupRequestID{t.Fatalf("unsafe recovery: %+v %v",result,err)}
}
func TestPartialFillThenCancelIsCumulative(t *testing.T){
 db,broker,preflight:=executionFixture();broker.state.Status="EXECUTION_REPORT_STATUS_PARTIALLYFILL";broker.state.LotsExecuted="1";broker.state.Total=tinvest.Money{Currency:"rub",Units:"100"};broker.state.Commission=tinvest.Money{Currency:"rub",Nano:50000000}
 result,err:=Perform(context.Background(),db,broker,db.intent.ID,preflight);if err!=nil||result.State!="partial"{t.Fatalf("%+v %v",result,err)}
 result,err=Cancel(context.Background(),db,broker,db.intent.ID)
 if err!=nil||result.State!="cancelled"||result.ExecutedLots!=1||result.CashDeltaNano!=-100050000000||broker.posts!=1||broker.cancels!=1{t.Fatalf("partial cancellation lost accounting: %+v %v",result,err)}
}
func TestPersistAndSubmissionGatesPrecedeBrokerCall(t *testing.T){
 for _,mode:=range []string{"prepare failure","paused gate","risk rejection"}{t.Run(mode,func(t *testing.T){
  db,broker,preflight:=executionFixture()
  switch mode{case "prepare failure":db.failPrepare=true;case "paused gate":db.gateClosed=true;case "risk rejection":preflight=func(context.Context,Intent)(Prepared,error){return Prepared{},fmt.Errorf("closed market")}}
  if _,err:=Perform(context.Background(),db,broker,db.intent.ID,preflight);err==nil||broker.posts!=0{t.Fatal("broker called before gates were durable")}
 })}
}
func TestNotFoundDoesNotAuthorizeResubmission(t *testing.T){
 db,broker,preflight:=executionFixture();prepared,_:=preflight(context.Background(),db.intent);_ = db.PrepareIntent(context.Background(),db.intent.ID,prepared);db.intent.State="submitting";broker.stateError=errors.New("order not found")
 for i:=0;i<2;i++{if _,err:=Perform(context.Background(),db,broker,db.intent.ID,preflight);err==nil{t.Fatal("not-found outcome was ignored")}}
 if broker.posts!=0||db.intent.State!="unknown"{t.Fatal("unknown request was resubmitted")}
}
func TestInvalidFillPausesAccounting(t *testing.T){
 for _,mode:=range []string{"price","commission","identity","quantity"}{t.Run(mode,func(t *testing.T){
  db,broker,preflight:=executionFixture()
  switch mode{case "price":broker.state.Total.Units="201";case "commission":broker.state.Commission.Nano=900000000;case "identity":broker.state.UID="other";case "quantity":broker.state.LotsExecuted="3"}
  if _,err:=Perform(context.Background(),db,broker,db.intent.ID,preflight);err==nil||db.intent.State!="unknown"{t.Fatal("inconsistent fill accepted")}
 })}
}
func TestTickRounding(t *testing.T){
 db,_,preflight:=executionFixture();p,_:=preflight(context.Background(),db.intent);p.Risk.EstimatedValueNano="200410000000"
 buy,err:=BuildLimit(db.intent,p.Snapshot,p.Risk);if err!=nil||buy.Price.Nano!=200000000{t.Fatalf("unsafe buy rounding: %+v %v",buy,err)}
 db.intent.Decision.Action="SELL";sell,err:=BuildLimit(db.intent,p.Snapshot,p.Risk);if err!=nil||sell.Price.Nano!=210000000{t.Fatalf("unsafe sell rounding: %+v %v",sell,err)}
 if buy.ConfirmMarginTrade||buy.OrderID!=db.intent.ID||buy.OrderType!="ORDER_TYPE_LIMIT"{t.Fatal("unsafe request defaults")}
}

func TestSellAccountingIncludesCommission(t *testing.T){
 db,broker,preflight:=executionFixture();p,_:=preflight(context.Background(),db.intent)
 db.intent.Decision.Action="SELL";p.Request.Direction="ORDER_DIRECTION_SELL";db.intent.Request=&p.Request;db.intent.Snapshot=&p.Snapshot
 broker.state.Direction="ORDER_DIRECTION_SELL";broker.state.Total=tinvest.Money{Currency:"rub",Units:"202"};broker.state.Commission=tinvest.Money{Currency:"rub",Nano:100000000}
 state,cash,fee,err:=ValidateState(db.intent,broker.state)
 if err!=nil||state!="filled"||cash!=201900000000||fee!=100000000{t.Fatalf("wrong sale accounting: state=%s cash=%d fee=%d err=%v",state,cash,fee,err)}
}

// Regression from the actual Sandbox fill and cash balance on 2026-10-26.
func TestSandboxObservedBuyCommissionIsSeparateFromTotal(t *testing.T){
 db,broker,preflight:=executionFixture();p,_:=preflight(context.Background(),db.intent)
 db.intent.Decision.Lots=1;p.Request.Price=tinvest.Money{Units:"292"};db.intent.Request=&p.Request;db.intent.Snapshot=&p.Snapshot
 broker.state.LotsRequested="1";broker.state.LotsExecuted="1"
 broker.state.Total=tinvest.Money{Currency:"rub",Units:"291",Nano:780000000}
 broker.state.Commission=tinvest.Money{Currency:"rub",Nano:145890000}
 _,cash,fee,err:=ValidateState(db.intent,broker.state)
 if err!=nil||cash!=-291925890000||fee!=145890000{t.Fatalf("observed Sandbox accounting: cash=%d fee=%d err=%v",cash,fee,err)}
}
