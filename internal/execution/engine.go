package execution

import (
 "context"
 "fmt"
 "strconv"
 "strings"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/tinvest"
)

// Perform never re-POSTs a submitting/unknown/acknowledged intent. Recovery is
// read-only by the stored request UUID, even when the broker reports not found.
func Perform(ctx context.Context,db Store,broker Broker,id string,preflight Preflight)(Intent,error){
 intent,err:=db.GetIntent(ctx,id);if err!=nil{return intent,err}
 if Terminal(intent.State){return intent,nil}
 if intent.State!="prepared"{return Reconcile(ctx,db,broker,id)}
 prepared,err:=preflight(ctx,intent)
 if err!=nil{if saveErr:=db.MarkOrderError(auditContext(),id,"blocked",err.Error());saveErr!=nil{return intent,saveErr};return intent,err}
 if err:=db.PrepareIntent(ctx,id,prepared);err!=nil{return intent,err}
 if err:=db.MarkSubmitting(ctx,id);err!=nil{return intent,err}
 response,postErr:=broker.PostLimit(ctx,prepared.Request)
 if postErr!=nil{
  if tinvest.DefiniteOrderRejection(postErr){
   if err:=db.MarkOrderError(auditContext(),id,"rejected",postErr.Error());err!=nil{return intent,err}
   return intent,fmt.Errorf("broker rejected the request without accepting an order: %w",postErr)
  }
  message:="submission outcome is unknown; reconcile the same intent; "+postErr.Error()
  if err:=db.MarkOrderError(auditContext(),id,"unknown",message);err!=nil{return intent,fmt.Errorf("could not record ambiguous submission for %s",id)}
  return intent,fmt.Errorf("%s",message)
 }
 if err:=db.SaveAcknowledgement(auditContext(),id,response.ID,response.Raw);err!=nil{return intent,fmt.Errorf("broker acknowledged order but database update failed; reconcile %s",id)}
 return Reconcile(ctx,db,broker,id)
}

// auditContext is used only for single bounded Store methods after interruption.
// Each concrete Store method applies its own timeout to this detached context.
func auditContext()context.Context{return context.Background()}

func Reconcile(ctx context.Context,db Store,broker Broker,id string)(Intent,error){
 intent,err:=db.GetIntent(ctx,id);if err!=nil{return intent,err}
 if intent.State=="blocked"||(Terminal(intent.State)&&intent.BrokerID==""){return intent,nil}
 if intent.State=="prepared"{return intent,fmt.Errorf("intent has never been submitted; execute it with a fresh preflight")}
 if intent.Request==nil{return intent,fmt.Errorf("intent has no persisted order request")}
 lookup:=intent.BrokerID;requestID:=false;if lookup==""{lookup=intent.ID;requestID=true}
 state,err:=broker.OrderState(ctx,intent.AccountID,lookup,requestID)
 if err!=nil{
  message:="cannot determine broker order state; no resubmission: "+err.Error()
  if saveErr:=db.MarkOrderError(auditContext(),id,"unknown",message);saveErr!=nil{return intent,saveErr};return intent,fmt.Errorf("%s",message)
 }
 if err:=db.SaveOrderObservation(auditContext(),id,state.Raw);err!=nil{return intent,err}
 status,cash,fee,err:=ValidateState(intent,state)
 if err!=nil{if saveErr:=db.MarkOrderError(auditContext(),id,"unknown",err.Error());saveErr!=nil{return intent,saveErr};return intent,err}
 if state.LotsExecuted==""{state.LotsExecuted="0"}
 if err:=db.SaveOrderState(ctx,id,status,state,cash,fee);err!=nil{return intent,err}
 return db.GetIntent(ctx,id)
}
func ValidateState(intent Intent,state tinvest.OrderState)(string,int64,int64,error){
 bad:=func(reason string)(string,int64,int64,error){return "",0,0,fmt.Errorf("inconsistent broker state: %s",reason)}
 request:=intent.Request;if request==nil{return bad("missing request")}
 executedText:=state.LotsExecuted;if executedText==""{executedText="0"}
 requested,e1:=strconv.ParseInt(state.LotsRequested,10,64);executed,e2:=strconv.ParseInt(executedText,10,64)
 if e1!=nil||e2!=nil||requested!=intent.Decision.Lots||executed<0||executed>requested||executed<intent.ExecutedLots{return bad("invalid or decreasing executed quantity")}
 if state.ID==""||(intent.BrokerID!=""&&state.ID!=intent.BrokerID)||state.UID!=request.InstrumentID||state.Direction!=request.Direction||state.OrderType!="ORDER_TYPE_LIMIT"{return bad("order identity does not match persisted intent")}
 status:=""
 switch state.Status{
 case "EXECUTION_REPORT_STATUS_FILL":if executed!=requested{return bad("filled order has unfilled lots")};status="filled"
 case "EXECUTION_REPORT_STATUS_CANCELLED":status="cancelled"
 case "EXECUTION_REPORT_STATUS_REJECTED":if executed!=0{return bad("rejected order has fills")};status="rejected"
 case "EXECUTION_REPORT_STATUS_NEW":if executed!=0{return bad("new order has fills")};status="active"
 case "EXECUTION_REPORT_STATUS_PARTIALLYFILL":if executed<=0||executed>=requested{return bad("invalid partial fill")};status="partial"
 default:return bad("unknown execution status")
 }
 if executed==0{fee,err:=amount.Parse(state.Commission);if err!=nil||fee!=0{return bad("unexpected commission on an unfilled Sandbox order")};return status,0,0,nil}
 if !strings.EqualFold(state.Total.Currency,"rub")||!strings.EqualFold(state.Commission.Currency,"rub"){return bad("non-RUB execution amount")}
 total,e1:=amount.Parse(state.Total);fee,e2:=amount.Parse(state.Commission)
 if e1!=nil||e2!=nil||total<=0||fee<0||fee>total{return bad("invalid execution amount/commission")}
 if intent.Snapshot==nil{return bad("missing instrument metadata")}
 var lot int64;for _,q:=range intent.Snapshot.Quotes{if q.Instrument.UID==request.InstrumentID{lot=int64(q.Instrument.Lot)}}
 if lot<=0{return bad("unknown lot size")}
 pieces,e:=amount.Mul(executed,lot);if e!=nil{return bad("execution quantity overflow")}
 limit,e:=amount.Parse(request.Price);if e!=nil||limit<=0{return bad("invalid persisted price")}
 bound,e:=amount.Mul(limit,pieces);if e!=nil{return bad("execution value overflow")}
 gross:=total-fee
 if (intent.Decision.Action=="BUY"&&gross>bound)||(intent.Decision.Action=="SELL"&&gross<bound){return bad("fill price exceeds persisted limit")}
 maxFee,e:=amount.BPS(gross,5,true);if e!=nil{return bad("fee overflow")};maxFee,e=amount.CeilKopeck(maxFee);if e!=nil||fee>maxFee{return bad("unexpected Sandbox commission")}
 // T-Invest totalOrderAmount includes commission. For SELL remove the fee
 // included in total to get gross, then deduct the fee paid from proceeds.
 cash:=-total
 if intent.Decision.Action=="SELL"{twice,e:=amount.Mul(fee,2);if e!=nil{return bad("fee overflow")};cash=total-twice;if cash<0{return bad("negative sale proceeds")}}
 return status,cash,fee,nil
}
func Cancel(ctx context.Context,db Store,broker Broker,id string)(Intent,error){
 intent,err:=db.GetIntent(ctx,id);if err!=nil{return intent,err};if Terminal(intent.State){return intent,nil}
 if intent.State=="prepared"||intent.Request==nil{return intent,fmt.Errorf("intent was never submitted")}
 lookup:=intent.BrokerID;requestID:=false;if lookup==""{lookup=intent.ID;requestID=true}
 state,err:=broker.OrderState(ctx,intent.AccountID,lookup,requestID);if err!=nil{return intent,err}
 if err:=db.SaveOrderObservation(auditContext(),id,state.Raw);err!=nil{return intent,err}
 requested,parseErr:=strconv.ParseInt(state.LotsRequested,10,64)
 if parseErr!=nil||requested!=intent.Decision.Lots||state.ID==""||state.UID!=intent.Request.InstrumentID||state.Direction!=intent.Request.Direction||state.OrderType!="ORDER_TYPE_LIMIT"||(intent.BrokerID!=""&&intent.BrokerID!=state.ID){return intent,fmt.Errorf("cannot cancel an order whose identity differs from intent")}
 if state.Status=="EXECUTION_REPORT_STATUS_FILL"||state.Status=="EXECUTION_REPORT_STATUS_CANCELLED"||state.Status=="EXECUTION_REPORT_STATUS_REJECTED"{return Reconcile(ctx,db,broker,id)}
 if err:=broker.CancelSandbox(ctx,intent.AccountID,state.ID);err!=nil{
  message:="cancel outcome unknown; reconcile same intent: "+err.Error()
  if saveErr:=db.MarkOrderError(auditContext(),id,"unknown",message);saveErr!=nil{return intent,saveErr};return intent,fmt.Errorf("%s",message)
 }
 return Reconcile(ctx,db,broker,id)
}
func BuildLimit(intent Intent,s analysis.Snapshot,report analysis.RiskReport)(tinvest.LimitRequest,error){
 var request tinvest.LimitRequest
 if !report.Allowed||report.Pieces<=0||intent.Decision.Action=="HOLD"{return request,fmt.Errorf("decision cannot produce an order")}
 var instrument *tinvest.Instrument
 for i:=range s.Quotes{if s.Quotes[i].Instrument.Ticker==intent.Decision.Ticker&&s.Quotes[i].Instrument.ClassCode==intent.Decision.ClassCode{instrument=&s.Quotes[i].Instrument;break}}
 if instrument==nil{return request,fmt.Errorf("instrument is missing")}
 tick,err:=amount.Parse(instrument.MinPriceIncrement);if err!=nil||tick<=0{return request,fmt.Errorf("instrument price increment is unknown")}
 value,err:=amount.Positive(report.EstimatedValueNano);if err!=nil||value%report.Pieces!=0{return request,fmt.Errorf("invalid risk price estimate")}
 price:=value/report.Pieces
 if intent.Decision.Action=="BUY"{price-=price%tick}else if price%tick!=0{price,err=amount.Add(price,tick-price%tick);if err!=nil{return request,err}}
 if price<=0{return request,fmt.Errorf("invalid tick-rounded limit price")}
 direction:="ORDER_DIRECTION_BUY";if intent.Decision.Action=="SELL"{direction="ORDER_DIRECTION_SELL"}
 request=tinvest.LimitRequest{AccountID:intent.AccountID,InstrumentID:instrument.UID,Quantity:strconv.FormatInt(intent.Decision.Lots,10),Price:tinvest.Money{Units:strconv.FormatInt(price/amount.Scale,10),Nano:int32(price%amount.Scale)},Direction:direction,OrderType:"ORDER_TYPE_LIMIT",OrderID:intent.ID,TimeInForce:"TIME_IN_FORCE_FILL_AND_KILL"}
 return request,nil
}
const MaxAnalysisAge = 10*time.Minute
