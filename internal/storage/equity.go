package storage

import (
 "context"
 "encoding/json"
 "fmt"
 "strconv"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
)

func (s *Store) RecordEquity(ctx context.Context,account,run string,when time.Time,current int64)(analysis.EquityMetrics,error){
 var metrics analysis.EquityMetrics
 if account==""||when.IsZero()||current<0{return metrics,fmt.Errorf("invalid equity observation")}
 when=when.UTC().Truncate(time.Microsecond)
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return metrics,fmt.Errorf("begin equity sample")};defer tx.Rollback()
 if _,err=tx.ExecContext(ctx,"SELECT pg_advisory_xact_lock(hashtext($1)::bigint)",account);err!=nil{return metrics,fmt.Errorf("lock account equity history")}
 if current==0{
  var count int;if err=tx.QueryRowContext(ctx,"SELECT count(*) FROM equity_samples WHERE account_id=$1",account).Scan(&count);err!=nil||count==0{return metrics,fmt.Errorf("fund the empty account before its first equity observation")}
 }
 if _,err=tx.ExecContext(ctx,`INSERT INTO equity_samples(account_id,run_id,observed_at,equity_nano)
 VALUES($1,NULLIF($2,'')::uuid,$3,$4::numeric) ON CONFLICT(run_id) DO NOTHING`,account,run,when,amount.Text(current));err!=nil{return metrics,fmt.Errorf("save equity sample; run db migrate first")}
 if run!=""{
  var savedAccount,savedEquity string;var savedAt time.Time
  if err=tx.QueryRowContext(ctx,"SELECT account_id,observed_at,equity_nano::text FROM equity_samples WHERE run_id=$1::uuid",run).Scan(&savedAccount,&savedAt,&savedEquity);err!=nil||savedAccount!=account||!savedAt.Equal(when)||savedEquity!=amount.Text(current){return metrics,fmt.Errorf("conflicting equity observation for analysis run")}
 }
 rows,err:=tx.QueryContext(ctx,`WITH origin AS(SELECT min(observed_at) AS started FROM equity_samples WHERE account_id=$1)
 SELECT e.observed_at,e.equity_nano::text,(e.equity_nano-coalesce((SELECT sum(f.amount_nano) FROM cashflows f,origin o WHERE f.account_id=e.account_id AND f.occurred_at>o.started AND f.occurred_at<=e.observed_at),0))::text
 FROM equity_samples e WHERE e.account_id=$1 AND e.observed_at<=$2 ORDER BY e.observed_at,e.id LIMIT 200001`,account,when)
 if err!=nil{return metrics,fmt.Errorf("read cash-flow-adjusted equity history")}
 points:=[]analysis.EquityPoint{}
 for rows.Next(){var p analysis.EquityPoint;var raw,adjusted string
  if rows.Scan(&p.At,&raw,&adjusted)!=nil{rows.Close();return metrics,fmt.Errorf("read equity point")}
  p.Raw,err=strconv.ParseInt(raw,10,64);if err!=nil{rows.Close();return metrics,fmt.Errorf("raw equity overflow")}
  p.Adjusted,err=strconv.ParseInt(adjusted,10,64);if err!=nil{rows.Close();return metrics,fmt.Errorf("adjusted equity overflow")};points=append(points,p)
 }
 scanErr:=rows.Err();rows.Close();if scanErr!=nil{return metrics,fmt.Errorf("equity history interrupted")};if len(points)>200000{return metrics,fmt.Errorf("equity history requires compaction before execution")}
 metrics,err=analysis.EquityFromPoints(points,int64(analysis.DefaultConstraints().InitialCapitalRUB)*amount.Scale);if err!=nil{return metrics,err}
 if err=tx.Commit();err!=nil{return metrics,fmt.Errorf("commit equity sample")};return metrics,nil
}
func (s *Store) EquityHistory(ctx context.Context,account string,limit int)(json.RawMessage,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 rows,err:=s.db.QueryContext(ctx,`SELECT row_to_json(r) FROM (
 SELECT observed_at,equity_nano::text AS equity_nano,run_id FROM equity_samples
 WHERE account_id=$1 ORDER BY observed_at DESC,id DESC LIMIT $2) r`,account,limit)
 if err!=nil{return nil,fmt.Errorf("read equity history; run db migrate first")};defer rows.Close()
 result:=[]json.RawMessage{}
 for rows.Next(){var data []byte;if err:=rows.Scan(&data);err!=nil{return nil,fmt.Errorf("decode equity history")};result=append(result,json.RawMessage(data))}
 if rows.Err()!=nil{return nil,fmt.Errorf("equity history interrupted")};return json.Marshal(result)
}
