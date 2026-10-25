package tinvest

import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "strings"
 "time"
)

const sandboxURL = "https://sandbox-invest-public-api.tbank.ru/rest/"
const sandboxService = "tinkoff.public.invest.api.contract.v1.SandboxService/"
const instrumentsService = "tinkoff.public.invest.api.contract.v1.InstrumentsService/"
const marketService = "tinkoff.public.invest.api.contract.v1.MarketDataService/"

type Client struct { token string; http *http.Client; timeout time.Duration }
func New(token string, timeout time.Duration) *Client {
 return &Client{token:token,http:&http.Client{Timeout:timeout,CheckRedirect:func(_ *http.Request,_ []*http.Request) error { return http.ErrUseLastResponse }},timeout:timeout}
}
func (c *Client) call(ctx context.Context, method string, input, output any, readOnly bool) error {
 data,err:=json.Marshal(input); if err!=nil { return err }
 attempts:=1; if readOnly { attempts=3 }
 for attempt:=0; attempt<attempts; attempt++ {
  if attempt>0 { timer:=time.NewTimer(time.Duration(attempt)*time.Second); select { case <-ctx.Done(): timer.Stop(); return ctx.Err(); case <-timer.C: } }
  err=c.once(ctx,method,data,output)
  if err==nil { return nil }
  e,ok:=err.(*apiError); if !ok || !readOnly || (e.status!=429 && e.status!=502 && e.status!=503 && e.status!=504) { return err }
 }
 return err
}
type apiError struct { status int; code,tracking,message string }
func (e *apiError) Error() string { return fmt.Sprintf("T-Invest HTTP %d (code=%s, tracking=%s): %s",e.status,e.code,e.tracking,e.message) }
func (c *Client) once(ctx context.Context, method string, data []byte, output any) error {
 ctx,cancel:=context.WithTimeout(ctx,c.timeout); defer cancel()
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,sandboxURL+method,bytes.NewReader(data)); if err!=nil { return err }
 req.Header.Set("Authorization","Bearer "+c.token); req.Header.Set("Content-Type","application/json"); req.Header.Set("Accept","application/json")
 resp,err:=c.http.Do(req); if err!=nil { return fmt.Errorf("Sandbox request failed: %w",err) }; defer resp.Body.Close()
 body,err:=io.ReadAll(io.LimitReader(resp.Body,4*1024*1024+1)); if err!=nil { return fmt.Errorf("read Sandbox response: %w",err) }
 if len(body)>4*1024*1024 { return fmt.Errorf("Sandbox response too large") }
 if resp.StatusCode<200 || resp.StatusCode>=300 {
  // Extract only the diagnostic message, redact the credential, and bound it.
  var payload struct { Code json.RawMessage `json:"code"`; Message string `json:"message"` }; _=json.Unmarshal(body,&payload)
  code:=strings.Trim(string(payload.Code),"\"")
  if len(code)>12 { code="" }; for _, r:=range code { if r<'0'||r>'9' { code=""; break } }
  message:=payload.Message
  if c.token!=""{message=strings.ReplaceAll(message,c.token,"[redacted]")}
  message=strings.Map(func(r rune)rune{if r<32||r==127{return ' '};return r},message)
  runes:=[]rune(message);if len(runes)>512{message=string(runes[:512])+"..."}
  return &apiError{status:resp.StatusCode,code:code,tracking:resp.Header.Get("x-tracking-id"),message:message}
 }
 if err:=json.Unmarshal(body,output); err!=nil { return fmt.Errorf("invalid Sandbox JSON response: %w",err) }; return nil
}
func (c *Client) Accounts(ctx context.Context) (Accounts,error) { var out Accounts; err:=c.call(ctx,sandboxService+"GetSandboxAccounts",struct{}{},&out,true); return out,err }
func (c *Client) Create(ctx context.Context,name string) (string,error) {
 var out struct { ID string `json:"accountId"` }; err:=c.call(ctx,sandboxService+"OpenSandboxAccount",map[string]string{"name":name},&out,false)
 if err==nil && out.ID=="" { err=fmt.Errorf("Sandbox returned empty account ID") }; return out.ID,err
}
func (c *Client) Fund(ctx context.Context,id string,amount Money) (Money,error) {
 var out struct { Balance Money `json:"balance"` }; err:=c.call(ctx,sandboxService+"SandboxPayIn",struct { ID string `json:"accountId"`; Amount Money `json:"amount"` }{id,amount},&out,false); return out.Balance,err
}
func (c *Client) Snapshot(ctx context.Context,id string,portfolio bool) (json.RawMessage,error) {
 method:="GetSandboxPositions"; if portfolio { method="GetSandboxPortfolio" }
 var out json.RawMessage; err:=c.call(ctx,sandboxService+method,map[string]string{"accountId":id},&out,true); return out,err
}
func (c *Client) Instrument(ctx context.Context,ticker,class string) (Instrument,error) {
 var out InstrumentResponse; err:=c.call(ctx,instrumentsService+"GetInstrumentBy",map[string]string{"idType":"INSTRUMENT_ID_TYPE_TICKER","id":ticker,"classCode":class},&out,true)
 if err==nil && (out.Instrument.UID=="" || out.Instrument.Lot<=0) { err=fmt.Errorf("incomplete instrument metadata") }; return out.Instrument,err
}
func (c *Client) Prices(ctx context.Context,ids []string) (Prices,error) {
 var out Prices; err:=c.call(ctx,marketService+"GetLastPrices",map[string]any{"instrumentId":ids,"lastPriceType":"LAST_PRICE_EXCHANGE","instrumentStatus":"INSTRUMENT_STATUS_ALL"},&out,true); return out,err
}
