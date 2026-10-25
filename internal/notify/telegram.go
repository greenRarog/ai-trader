// Package notify sends plain-text operational messages, never trading commands.
package notify

import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "time"
)
type Client struct{token,chat string;http *http.Client;endpoint string}
type Failure struct{RetryAfter time.Duration;Rejected bool;message string}
func(f *Failure)Error()string{return f.message}
func New(token,chat string,timeout time.Duration)*Client{return &Client{token:token,chat:chat,endpoint:"https://api.telegram.org",http:&http.Client{Timeout:timeout,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}}}
func(c *Client)Send(ctx context.Context,text string)error{
 if len([]rune(text))==0||len([]rune(text))>3000{return &Failure{Rejected:true,message:"notification text length is invalid"}}
 body,_:=json.Marshal(map[string]any{"chat_id":c.chat,"text":text,"link_preview_options":map[string]bool{"is_disabled":true}})
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.endpoint+"/bot"+c.token+"/sendMessage",bytes.NewReader(body));if err!=nil{return fmt.Errorf("create Telegram request")};req.Header.Set("Content-Type","application/json")
 response,err:=c.http.Do(req);if err!=nil{return &Failure{message:"Telegram delivery outcome unknown; not automatically retried"}};defer response.Body.Close()
 raw,err:=io.ReadAll(io.LimitReader(response.Body,65537));if err!=nil||len(raw)>65536{return &Failure{message:"Telegram response unreadable; delivery outcome unknown"}}
 var result struct{OK bool `json:"ok"`;Code int `json:"error_code"`;Parameters struct{Retry int `json:"retry_after"`} `json:"parameters"`}
 if json.Unmarshal(raw,&result)!=nil{return &Failure{message:"Telegram response invalid; delivery outcome unknown"}}
 if response.StatusCode==http.StatusOK&&result.OK{return nil}
 // Only explicit not-ok responses with a 4xx code prove rejection. Never log
 // the request URL, token, response description or recipient in errors.
 if !result.OK&&response.StatusCode>=400&&response.StatusCode<500{
  retry:=time.Duration(0);if response.StatusCode==429&&result.Parameters.Retry>0{retry=time.Duration(result.Parameters.Retry)*time.Second;if retry>time.Hour{retry=time.Hour}}
  return &Failure{Rejected:true,RetryAfter:retry,message:fmt.Sprintf("Telegram rejected notification (HTTP %d)",response.StatusCode)}
 }
 return &Failure{message:"Telegram delivery outcome unknown; inspect outbox"}
}
