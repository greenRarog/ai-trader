package tinvest

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"
 "time"
)
// Route to a local fake server while production URLs remain hardcoded.
type transport struct { url string; base http.RoundTripper }
func (r transport) RoundTrip(req *http.Request) (*http.Response,error) {
 req=req.Clone(req.Context()); u:=*req.URL; req.URL=&u
 req.URL.Scheme="http"; req.URL.Host=strings.TrimPrefix(r.url,"http://")
 return r.base.RoundTrip(req)
}
func fakeClient(t *testing.T,handler http.HandlerFunc) *Client {
 t.Helper(); server:=httptest.NewServer(handler); t.Cleanup(server.Close)
 c:=New("test-secret",time.Second); c.http.Transport=transport{server.URL,http.DefaultTransport}; return c
}
func TestWireFormat(t *testing.T) {
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request) {
  if r.Header.Get("Authorization")!="Bearer test-secret" { t.Error("missing auth") }
  if r.Method!="POST" { t.Error("wrong method") }
  w.Header().Set("Content-Type","application/json")
  switch r.URL.Path {
  case "/rest/"+sandboxService+"GetSandboxAccounts":
   io.WriteString(w,`{"accounts":[{"id":"sandbox-id","status":"ACCOUNT_STATUS_OPEN"}]}`)
  case "/rest/"+sandboxService+"SandboxPayIn":
   var body struct { ID string `json:"accountId"`; Amount Money `json:"amount"` }
   if err:=json.NewDecoder(r.Body).Decode(&body); err!=nil { t.Error(err) }
   if body.ID!="sandbox-id" || body.Amount.Units!="100000" || body.Amount.Currency!="rub" { t.Errorf("wrong request: %+v",body) }
   io.WriteString(w,`{"balance":{"currency":"rub","units":"100000","nano":0}}`)
  case "/rest/"+instrumentsService+"GetInstrumentBy":
   var body map[string]string; _=json.NewDecoder(r.Body).Decode(&body)
   if body["id"]!="SBER" || body["classCode"]!="TQBR" || body["idType"]!="INSTRUMENT_ID_TYPE_TICKER" { t.Error("wrong instrument request") }
   io.WriteString(w,`{"instrument":{"uid":"uid","ticker":"SBER","classCode":"TQBR","lot":10,"instrumentType":"share","currency":"rub"}}`)
  case "/rest/"+marketService+"GetLastPrices":
   var body struct { IDs []string `json:"instrumentId"` }; _=json.NewDecoder(r.Body).Decode(&body)
   if len(body.IDs)!=1 || body.IDs[0]!="uid" { t.Error("wrong quote request") }
   io.WriteString(w,`{"lastPrices":[{"instrumentUid":"uid","price":{"units":"250","nano":100000000},"time":"2026-10-23T15:00:00Z"}]}`)
  default: t.Errorf("unexpected method: %s",r.URL.Path); w.WriteHeader(404)
  }
 })
 ctx:=context.Background()
 accounts,err:=c.Accounts(ctx); if err!=nil || len(accounts.Accounts)!=1 { t.Fatalf("%+v %v",accounts,err) }
 amount,_:=ParseDeposit("100000"); balance,err:=c.Fund(ctx,"sandbox-id",amount); if err!=nil || balance.Units!="100000" { t.Fatalf("%+v %v",balance,err) }
 instrument,err:=c.Instrument(ctx,"SBER","TQBR"); if err!=nil || instrument.Kind!="share" { t.Fatalf("%+v %v",instrument,err) }
 prices,err:=c.Prices(ctx,[]string{"uid"}); if err!=nil || len(prices.Prices)!=1 { t.Fatalf("%+v %v",prices,err) }
}
func TestMutationNeverRetried(t *testing.T) {
 var calls atomic.Int32
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request) { calls.Add(1); w.WriteHeader(503); io.WriteString(w,`{"code":14,"message":"test-secret"}`) })
 _,err:=c.Create(context.Background(),"test")
 if err==nil || calls.Load()!=1 { t.Fatalf("calls=%d err=%v",calls.Load(),err) }
 if strings.Contains(err.Error(),"test-secret") { t.Fatal("secret leaked") }
 calls.Store(0); amount,_:=ParseDeposit("100000"); _,err=c.Fund(context.Background(),"id",amount)
 if err==nil || calls.Load()!=1 { t.Fatalf("calls=%d err=%v",calls.Load(),err) }
}
func TestReadRetryAndAuthentication(t *testing.T) {
 var calls atomic.Int32
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request) {
  if calls.Add(1)==1 { w.WriteHeader(503); return }; io.WriteString(w,`{"accounts":[]}`)
 })
 if _,err:=c.Accounts(context.Background()); err!=nil || calls.Load()!=2 { t.Fatalf("calls=%d err=%v",calls.Load(),err) }
 calls.Store(0)
 c=fakeClient(t,func(w http.ResponseWriter,r *http.Request) { calls.Add(1); w.WriteHeader(401) })
 if _,err:=c.Accounts(context.Background()); err==nil || calls.Load()!=1 { t.Fatalf("calls=%d err=%v",calls.Load(),err) }
}
func TestCancellationAndMalformedResponse(t *testing.T) {
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request) { io.WriteString(w,`not JSON`) })
 if _,err:=c.Accounts(context.Background()); err==nil { t.Fatal("accepted malformed response") }
 ctx,cancel:=context.WithCancel(context.Background()); cancel()
 if _,err:=c.Accounts(ctx); err==nil { t.Fatal("ignored cancellation") }
 // Release the handler explicitly before httptest.Server.Close runs, even
 // when request cancellation is not observed by the server.
 release := make(chan struct{})
 defer close(release)
 c = fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
  // Consume the POST body so the server can detect a disconnected client.
  _, _ = io.Copy(io.Discard, r.Body)
  select {
  case <-r.Context().Done():
  case <-release:
  }
 })
 c.timeout = 20 * time.Millisecond
 if _, err := c.Accounts(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
  t.Fatalf("expected deadline exceeded, got %v", err)
 }
}

func TestAPIArgumentErrorPreservesSafeMessage(t *testing.T){
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request){w.WriteHeader(400);io.WriteString(w,`{"code":3,"message":"invalid from timestamp; token test-secret"}`)})
 _,err:=c.Candles(context.Background(),"uid",time.Now().Add(-time.Hour),time.Now())
 if err==nil||!strings.Contains(err.Error(),"invalid from timestamp")||strings.Contains(err.Error(),"test-secret"){t.Fatalf("unsafe or unhelpful diagnostic: %v",err)}
}
