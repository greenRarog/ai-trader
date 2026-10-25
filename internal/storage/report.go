package storage

import (
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "strings"
 "time"
 "ai-trader/internal/amount"
 "ai-trader/internal/execution"
 "ai-trader/internal/report"
)

// ReportData reads a consistent snapshot; it does not sample or mutate equity.
func(s *Store)ReportData(ctx context.Context,account string,to time.Time)(report.Data,error){
 var data report.Data
 ctx,cancel:=context.WithTimeout(ctx,30*time.Second);defer cancel()
 tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{Isolation:sql.LevelRepeatableRead,ReadOnly:true});if err!=nil{return data,fmt.Errorf("begin analytics snapshot")};defer tx.Rollback()
 rows,err:=tx.QueryContext(ctx,"SELECT observed_at,equity_nano::bigint FROM equity_samples WHERE account_id=$1 AND observed_at<=$2 ORDER BY observed_at,id LIMIT 200001",account,to);if err!=nil{return data,fmt.Errorf("read report equity")}
 for rows.Next(){var e report.Equity;if err=rows.Scan(&e.At,&e.Value);err!=nil{rows.Close();return data,fmt.Errorf("decode report equity")};data.Equity=append(data.Equity,e)};err=rows.Err();rows.Close();if err!=nil{return data,err};if len(data.Equity)>200000{return data,fmt.Errorf("report history requires compaction")}
 rows,err=tx.QueryContext(ctx,"SELECT occurred_at,amount_nano::bigint FROM cashflows WHERE account_id=$1 AND occurred_at<=$2 ORDER BY occurred_at,id LIMIT 200001",account,to);if err!=nil{return data,fmt.Errorf("read report cash flows")}
 for rows.Next(){var f report.Flow;if err=rows.Scan(&f.At,&f.Amount);err!=nil{rows.Close();return data,fmt.Errorf("decode report cash flow")};data.Flows=append(data.Flows,f)};err=rows.Err();rows.Close();if err!=nil{return data,err};if len(data.Flows)>200000{return data,fmt.Errorf("too many report cash flows")}
 rows,err=tx.QueryContext(ctx,`SELECT row_to_json(i) FROM order_intents i WHERE account_id=$1 AND created_at<=$2 AND (executed_lots>0 OR state IN ('submitting','acknowledged','unknown','active','partial')) ORDER BY created_at,id LIMIT 50001`,account,to);if err!=nil{return data,fmt.Errorf("read report executions")}
 count:=0
 for rows.Next(){
  count++;var raw []byte;var intent execution.Intent
  if rows.Scan(&raw)!=nil||json.Unmarshal(raw,&intent)!=nil{rows.Close();return data,fmt.Errorf("decode report execution")}
  if intent.ExecutedLots==0{data.Issues=append(data.Issues,"unresolved order: "+intent.ID);continue}
  if intent.Request==nil||intent.Snapshot==nil{rows.Close();return data,fmt.Errorf("execution is missing lot metadata")}
  var lot int64;for _,q:=range intent.Snapshot.Quotes{if q.Instrument.UID==intent.Request.InstrumentID{lot=int64(q.Instrument.Lot)}}
  pieces,err:=amount.Mul(intent.ExecutedLots,lot);if err!=nil||lot<=0{rows.Close();return data,fmt.Errorf("invalid execution lot quantity")}
  data.Fills=append(data.Fills,report.Fill{ID:intent.ID,UID:intent.Request.InstrumentID,Ticker:intent.Decision.Ticker,Action:intent.Decision.Action,State:intent.State,At:intent.CreatedAt,Pieces:pieces,Cash:intent.CashDeltaNano,Fee:intent.CommissionNano})
 };err=rows.Err();rows.Close();if err!=nil{return data,err};if count>50000{return data,fmt.Errorf("too many report executions")}
 var unresolved int
 if err=tx.QueryRowContext(ctx,"SELECT count(*) FROM funding_intents WHERE account_id=$1 AND state NOT IN ('confirmed','abandoned')",account).Scan(&unresolved);err!=nil{return data,fmt.Errorf("read report funding status")};if unresolved>0{data.Issues=append(data.Issues,"unresolved funding; external flows/performance may be incomplete")}
 var reason string;var initialized bool
 err=tx.QueryRowContext(ctx,"SELECT pause_reason,anchor_at IS NOT NULL FROM trading_accounts WHERE account_id=$1",account).Scan(&reason,&initialized)
 if err==sql.ErrNoRows||err==nil&&!initialized{data.Issues=append(data.Issues,"account tracking not initialized")}else if err!=nil{return data,fmt.Errorf("read accounting status")}
 if strings.Contains(reason,"mismatch"){data.Issues=append(data.Issues,"account ledger mismatch requires reconciliation")}
 var snapshotIssues []byte
 err=tx.QueryRowContext(ctx,"SELECT snapshot->'account'->'issues' FROM analysis_runs WHERE account_id=$1 AND snapshot->'account' IS NOT NULL ORDER BY created_at DESC LIMIT 1",account).Scan(&snapshotIssues)
 if err!=nil&&err!=sql.ErrNoRows{return data,fmt.Errorf("read latest account issues")};var issues []string
 if len(snapshotIssues)>0&&json.Unmarshal(snapshotIssues,&issues)==nil&&len(issues)>0{data.Issues=append(data.Issues,"latest account snapshot has unresolved issues")}
 if err=tx.Commit();err!=nil{return data,fmt.Errorf("commit analytics snapshot")};return data,nil
}
