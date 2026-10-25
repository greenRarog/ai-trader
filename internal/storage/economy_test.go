package storage

import (
 "context"
 "encoding/json"
 "strings"
 "sync"
 "testing"
 "time"

 "ai-trader/internal/analysis"
)
func economyRun(t *testing.T,db *Store,ctx context.Context,account string)string{
 t.Helper();id,err:=db.Begin(ctx,account,"codex_cli","test",analysis.PromptVersion);if err!=nil{t.Fatal(err)}
 if err:=db.Prepare(ctx,id,analysis.Snapshot{},json.RawMessage(`{"test":true}`));err!=nil{t.Fatal(err)};return id
}
func TestModelBudgetDuplicateAndRefresh(t *testing.T){
 db,ctx,account:=executionTestDB(t);fingerprint:=strings.Repeat("a",64)
 first:=economyRun(t,db,ctx,account)
 r,err:=db.ReserveModel(ctx,first,fingerprint,false,2,10*time.Minute);if err!=nil||!r.Attempt{t.Fatalf("first: %+v %v",r,err)}
 if _,err:=db.ReserveModel(ctx,first,fingerprint,false,2,10*time.Minute);err==nil{t.Fatal("same run reserved twice")}
 result:=analysis.Result{Decision:&analysis.Decision{Action:"BUY",Ticker:"SBER",ClassCode:"TQBR",Lots:1,Reason:"test"},Usage:&analysis.Usage{InputTokens:100,OutputTokens:10,TotalTokens:110}}
 if err:=db.Finish(ctx,first,"validated_proposal",result,"");err!=nil{t.Fatal(err)}
 second:=economyRun(t,db,ctx,account)
 r,err=db.ReserveModel(ctx,second,fingerprint,false,2,10*time.Minute);if err!=nil||r.Attempt||r.SourceID!=first||r.SourceResult.Decision==nil{t.Fatalf("duplicate: %+v %v",r,err)}
 // A reused row is diagnostic only; it cannot create another executable intent.
 if err:=db.Finish(ctx,second,"reused",analysis.Result{Decision:result.Decision},"");err!=nil{t.Fatal(err)}
 if _,err:=db.CreateIntent(ctx,account,second);err==nil{t.Fatal("reused result became another executable proposal")}
 third:=economyRun(t,db,ctx,account)
 r,err=db.ReserveModel(ctx,third,strings.Repeat("b",64),false,2,10*time.Minute);if err!=nil||!r.Attempt{t.Fatalf("second paid attempt: %+v %v",r,err)}
 // Failure or uncertain outcome still consumes the daily allowance.
 if err:=db.Finish(ctx,third,"model_error",analysis.Result{},"timeout");err!=nil{t.Fatal(err)}
 fourth:=economyRun(t,db,ctx,account)
 r,err=db.ReserveModel(ctx,fourth,strings.Repeat("c",64),false,2,10*time.Minute);if err!=nil||r.Attempt||r.SourceID!=""{t.Fatalf("quota: %+v %v",r,err)}
 r,err=db.ReserveModel(ctx,fourth,strings.Repeat("c",64),true,2,10*time.Minute);if err!=nil||!r.Attempt{t.Fatalf("explicit refresh: %+v %v",r,err)}
 fifth:=economyRun(t,db,ctx,account)
 r,err=db.ReserveModel(ctx,fifth,strings.Repeat("b",64),false,2,10*time.Minute);if err!=nil||r.Attempt||r.SourceStatus!="model_error"{t.Fatalf("failed duplicate retried: %+v %v",r,err)}
 data,err:=db.UsageReport(ctx,account,7);if err!=nil||!json.Valid(data){t.Fatalf("report: %s %v",data,err)}
 var rows []struct{Attempts int `json:"model_attempts"`;Total string `json:"total_tokens"`;Reused int `json:"reused"`}
 if json.Unmarshal(data,&rows)!=nil||len(rows)!=1||rows[0].Attempts!=3||rows[0].Total!="110"||rows[0].Reused!=1{t.Fatalf("usage counted cached results twice or lost uncertain attempts: %s",data)}
}
func TestConcurrentDailyReservationAndDurableSlots(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 one:=economyRun(t,db,ctx,account);two:=economyRun(t,db,ctx,account)
 var wg sync.WaitGroup;results:=make(chan analysis.Reservation,2);errs:=make(chan error,2)
 for i,id:=range []string{one,two}{wg.Add(1);go func(id string,char string){defer wg.Done();r,err:=db.ReserveModel(ctx,id,strings.Repeat(char,64),false,1,10*time.Minute);results<-r;errs<-err}(id,[]string{"a","b"}[i])};wg.Wait();close(results);close(errs)
 count:=0;for r:=range results{if r.Attempt{count++}};for err:=range errs{if err!=nil{t.Fatal(err)}};if count!=1{t.Fatalf("concurrent attempts=%d",count)}
 key:="2026-10-26:after_open"
 claimed,err:=db.ClaimAnalysisSlot(ctx,account,key);if err!=nil||!claimed{t.Fatal("slot not persisted",err)}
 claimed,err=db.ClaimAnalysisSlot(ctx,account,key);if err!=nil||claimed{t.Fatal("same slot retried",err)}
 if _,err:=db.db.ExecContext(ctx,"DELETE FROM analysis_slots WHERE account_id=$1",account);err!=nil{t.Fatal(err)}
}

func TestBudgetResetsAtMoscowMidnight(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 yesterday:=economyRun(t,db,ctx,account)
 if r,err:=db.ReserveModel(ctx,yesterday,strings.Repeat("a",64),false,1,10*time.Minute);err!=nil||!r.Attempt{t.Fatal(err)}
 moscow:=time.FixedZone("Europe/Moscow",3*3600);now:=time.Now().In(moscow)
 boundary:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,moscow)
 if _,err:=db.db.ExecContext(ctx,"UPDATE analysis_runs SET model_attempted_at=$2 WHERE id=$1::uuid",yesterday,boundary.Add(-time.Second));err!=nil{t.Fatal(err)}
 today:=economyRun(t,db,ctx,account)
 if r,err:=db.ReserveModel(ctx,today,strings.Repeat("b",64),false,1,10*time.Minute);err!=nil||!r.Attempt{t.Fatalf("yesterday consumed today's quota: %+v %v",r,err)}
}
