package notify

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "strings"
 "testing"
 "time"
)
type roundTrip func(*http.Request)(*http.Response,error)
func(f roundTrip)RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
func TestPlainTextDeliveryAndNoCredentialInBody(t *testing.T){
 c:=New("123:secret","42",time.Second);calls:=0
 c.http.Transport=roundTrip(func(r *http.Request)(*http.Response,error){calls++;var body map[string]any;raw,_:=io.ReadAll(r.Body);json.Unmarshal(raw,&body)
  if body["text"]!="<test>"||body["chat_id"]!="42"||body["parse_mode"]!=nil||strings.Contains(string(raw),"secret"){t.Fatal("incorrect Telegram payload")}
  return &http.Response{StatusCode:200,Body:io.NopCloser(strings.NewReader(`{"ok":true}`))},nil
 })
 if err:=c.Send(context.Background(),"<test>");err!=nil||calls!=1{t.Fatal(err,calls)}
}
func TestTelegramErrorsAreSanitizedAndNeverRetriedInClient(t *testing.T){
 for _,tt:=range []struct{status int;body string;reject bool;retry time.Duration}{{429,`{"ok":false,"description":"secret","parameters":{"retry_after":60}}`,true,time.Minute},{403,`{"ok":false,"description":"secret"}`,true,0},{500,`{"ok":false,"description":"secret"}`,false,0},{200,`garbage secret`,false,0}}{
  c:=New("123:secret","42",time.Second);calls:=0;c.http.Transport=roundTrip(func(*http.Request)(*http.Response,error){calls++;return &http.Response{StatusCode:tt.status,Body:io.NopCloser(strings.NewReader(tt.body))},nil})
  err:=c.Send(context.Background(),"test");var f *Failure
  if !errors.As(err,&f)||f.Rejected!=tt.reject||f.RetryAfter!=tt.retry||calls!=1||strings.Contains(err.Error(),"secret"){t.Fatalf("bad delivery outcome: %v calls %d",err,calls)}
 }
 c:=New("123:secret","42",time.Second);c.http.Transport=roundTrip(func(*http.Request)(*http.Response,error){return nil,errors.New("https://token-secret")})
 if err:=c.Send(context.Background(),"test");err==nil||strings.Contains(err.Error(),"secret"){t.Fatal("network error leaked URL")}
}
