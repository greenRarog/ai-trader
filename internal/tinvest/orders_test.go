package tinvest

import (
 "context"
 "encoding/json"
 "io"
 "net/http"
 "testing"
)
func TestSandboxOrderWireAndNoPostRetry(t *testing.T){
 posts:=0
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request){
  var body map[string]json.RawMessage;_ = json.NewDecoder(r.Body).Decode(&body)
  switch r.URL.Path{
  case "/rest/"+sandboxService+"PostSandboxOrder":
   posts++;if string(body["orderId"])!=`"request-uuid"`||string(body["orderType"])!=`"ORDER_TYPE_LIMIT"`||string(body["confirmMarginTrade"])!="false"{t.Error("unsafe submission contract")}
   w.WriteHeader(503);io.WriteString(w,`{"code":14,"message":"unavailable"}`)
  case "/rest/"+sandboxService+"GetSandboxOrderState":
   if string(body["orderIdType"])!=`"ORDER_ID_TYPE_REQUEST"`{t.Error("request-ID recovery was not used")};io.WriteString(w,`{"orderId":"exchange","instrumentUid":"uid","lotsRequested":"1","lotsExecuted":"0","executionReportStatus":"EXECUTION_REPORT_STATUS_NEW"}`)
  case "/rest/"+sandboxService+"CancelSandboxOrder":io.WriteString(w,`{}`)
  default:t.Error("non-Sandbox order endpoint")
  }
 })
 request:=LimitRequest{AccountID:"sandbox",InstrumentID:"uid",Quantity:"1",Price:Money{Units:"100"},Direction:"ORDER_DIRECTION_BUY",OrderType:"ORDER_TYPE_LIMIT",OrderID:"request-uuid",TimeInForce:"TIME_IN_FORCE_FILL_AND_KILL"}
 if _,err:=c.PostLimit(context.Background(),request);err==nil||posts!=1{t.Fatal("POST was retried or failure ignored")}
 if _,err:=c.OrderState(context.Background(),"sandbox","request-uuid",true);err!=nil{t.Fatal(err)}
 if err:=c.CancelSandbox(context.Background(),"sandbox","exchange");err!=nil{t.Fatal(err)}
 request.ConfirmMarginTrade=true;if _,err:=c.PostLimit(context.Background(),request);err==nil||posts!=1{t.Fatal("margin request reached broker")}
}
func TestOnlyExplicitArgumentAndAuthRejectionsAreDefinitive(t *testing.T){
 if !DefiniteOrderRejection(&apiError{status:400,code:"3"})||DefiniteOrderRejection(&apiError{status:503,code:"14"}){t.Fatal("unsafe rejection classification")}
}
