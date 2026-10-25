package storage

import (
 "encoding/json"
 "testing"
 "time"
 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/report"
 "ai-trader/internal/tinvest"
)
func TestReportReadsConsistentJournalAndDoesNotCreateObservations(t *testing.T){
 db,ctx,account:=executionTestDB(t);now:=time.Now().UTC();initial:=&analysis.AccountState{EquityNano:100000*amount.Scale,AvailableRUBNano:100000*amount.Scale}
 if err:=db.InitializeAccount(ctx,account,initial,now.Add(-time.Hour));err!=nil{t.Fatal(err)}
 if _,err:=db.RecordEquity(ctx,account,"",now.Add(-time.Minute),initial.EquityNano);err!=nil{t.Fatal(err)}
 if err:=db.Resume(ctx,account);err!=nil{t.Fatal(err)}
 intent:=prepareOrder(t,db,ctx,account);if err:=db.MarkSubmitting(ctx,intent.ID);err!=nil{t.Fatal(err)}
 state:=tinvest.OrderState{ID:"test-exchange",LotsExecuted:"2",Raw:json.RawMessage(`{"lotsExecuted":"2"}`)}
 if err:=db.SaveOrderState(ctx,intent.ID,"filled",state,-200100000000,100000000);err!=nil{t.Fatal(err)}
 data,err:=db.ReportData(ctx,account,time.Now().UTC());if err!=nil{t.Fatal(err)}
 if len(data.Equity)!=1||len(data.Fills)!=1||data.Fills[0].Pieces!=2||data.Fills[0].Fee!=100000000{t.Fatalf("journal lost data: %+v",data)}
 summary,err:=report.Build(data,now.Add(-24*time.Hour),time.Now().UTC());if err!=nil||!summary.JournalComplete||summary.Fees!=100000000||len(summary.Positions)!=1{t.Fatalf("bad report: %+v %v",summary,err)}
 again,err:=db.ReportData(ctx,account,time.Now().UTC());if err!=nil||len(again.Equity)!=1{t.Fatal("report mutated observations",err)}
}
func TestNotificationOutboxDeduplicatesAndDoesNotResendAmbiguousDelivery(t *testing.T){
 db,ctx,account:=executionTestDB(t)
 if err:=db.EnqueueNotice(ctx,account,"same-event","42","test");err!=nil{t.Fatal(err)}
 if err:=db.EnqueueNotice(ctx,account,"same-event","42","test");err!=nil{t.Fatal(err)}
 n,found,err:=db.ClaimNotice(ctx,account,"wrong-chat");if err!=nil||found{t.Fatal("notification recipient changed",err)}
 n,found,err=db.ClaimNotice(ctx,account,"42");if err!=nil||!found||n.Attempts!=1{t.Fatal("missing notice",err)}
 if _,found,err:=db.ClaimNotice(ctx,account,"42");err!=nil||found{t.Fatal("sending notice claimed twice",err)}
 if err:=db.FinishNotice(ctx,n.ID,"unknown","timeout",0);err!=nil{t.Fatal(err)}
 if _,found,err:=db.ClaimNotice(ctx,account,"42");err!=nil||found{t.Fatal("ambiguous notice automatically retried",err)}
 data,err:=db.NoticeHistory(ctx,account);if err!=nil||!json.Valid(data){t.Fatal(err)}
 if err:=db.EnqueueNotice(ctx,account,"rate-limit","42","test");err!=nil{t.Fatal(err)}
 n,found,err=db.ClaimNotice(ctx,account,"42");if err!=nil||!found{t.Fatal(err)}
 if err:=db.FinishNotice(ctx,n.ID,"queued","rate limit",time.Hour);err!=nil{t.Fatal(err)}
 if _,found,err:=db.ClaimNotice(ctx,account,"42");err!=nil||found{t.Fatal("ignored rate-limit delay",err)}
}
