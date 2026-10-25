package storage

import (
 "context"
 "crypto/rand"
 "database/sql/driver"
 "encoding/json"
 "fmt"
 "time"

 "ai-trader/internal/analysis"
 "ai-trader/internal/execution"
 "ai-trader/internal/tinvest"
)

func UUID()(string,error){var b [16]byte;if _,err:=rand.Read(b[:]);err!=nil{return "",err};b[6]=(b[6]&15)|64;b[8]=(b[8]&63)|128;return fmt.Sprintf("%x-%x-%x-%x-%x",b[:4],b[4:6],b[6:8],b[8:10],b[10:]),nil}
func(s *Store)AcquireAccount(ctx context.Context,account string)(func(),error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 conn,err:=s.db.Conn(ctx);if err!=nil{return nil,fmt.Errorf("acquire account connection")}
 var locked bool
 if err=conn.QueryRowContext(ctx,"SELECT pg_try_advisory_lock(hashtextextended($1,0))","ai-trader-execution:"+account).Scan(&locked);err!=nil||!locked{conn.Close();return nil,fmt.Errorf("account is busy; another analysis, funding or execution is running")}
 return func(){cleanup,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();_,err:=conn.ExecContext(cleanup,"SELECT pg_advisory_unlock(hashtextextended($1,0))","ai-trader-execution:"+account);if err!=nil{conn.Raw(func(any)error{return driver.ErrBadConn})};conn.Close()},nil
}
func(s *Store)CreateIntent(ctx context.Context,account,analysisID string)(execution.Intent,error){
 var intent execution.Intent
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 // Returning an existing intent prevents any second UUID for the same decision.
 var existing string
 err:=s.db.QueryRowContext(ctx,"SELECT id::text FROM order_intents WHERE analysis_id=$1::uuid AND account_id=$2",analysisID,account).Scan(&existing)
 if err==nil{return s.GetIntent(ctx,existing)}
 var provider,status,sourceAccount string;var created time.Time;var raw []byte
 if err=s.db.QueryRowContext(ctx,"SELECT account_id,provider,status,created_at,result FROM analysis_runs WHERE id=$1::uuid",analysisID).Scan(&sourceAccount,&provider,&status,&created,&raw);err!=nil{return intent,fmt.Errorf("analysis was not found")}
 var result analysis.Result
 if json.Unmarshal(raw,&result)!=nil||sourceAccount!=account||status!="validated_proposal"||(provider!="codex_cli"&&provider!="openai_api")||result.Decision==nil||result.Risk==nil||!result.Risk.Allowed||(result.Decision.Action!="BUY"&&result.Decision.Action!="SELL"){return intent,fmt.Errorf("only a validated BUY/SELL decision from an AI provider can be executed")}
 if time.Since(created)>execution.MaxAnalysisAge||created.After(time.Now().Add(time.Minute)){return intent,fmt.Errorf("analysis is older than 10 minutes; run a new analyze")}
 id,err:=UUID();if err!=nil{return intent,err};decision,_:=json.Marshal(result.Decision)
 _,err=s.db.ExecContext(ctx,`INSERT INTO order_intents(id,analysis_id,account_id,state,decision) VALUES($1,$2,$3,'prepared',$4::jsonb) ON CONFLICT(analysis_id) DO NOTHING`,id,analysisID,account,string(decision))
 if err!=nil{return intent,fmt.Errorf("persist Sandbox order intent failed; run db migrate")}
 if err=s.db.QueryRowContext(ctx,"SELECT id::text FROM order_intents WHERE analysis_id=$1::uuid AND account_id=$2",analysisID,account).Scan(&id);err!=nil{return intent,fmt.Errorf("read persisted intent")};return s.GetIntent(ctx,id)
}
func(s *Store)GetIntent(ctx context.Context,id string)(execution.Intent,error){
 var out execution.Intent;var data []byte
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 if err:=s.db.QueryRowContext(ctx,"SELECT row_to_json(r) FROM(SELECT i.*,a.created_at AS analysis_created_at FROM order_intents i JOIN analysis_runs a ON a.id=i.analysis_id WHERE i.id=$1::uuid) r",id).Scan(&data);err!=nil{return out,fmt.Errorf("order intent not found")}
 if json.Unmarshal(data,&out)!=nil{return out,fmt.Errorf("invalid saved order intent")};return out,nil
}
func(s *Store)PrepareIntent(ctx context.Context,id string,prepared execution.Prepared)error{
 request,err:=json.Marshal(prepared.Request);if err!=nil{return err};snapshot,err:=json.Marshal(prepared.Snapshot);if err!=nil{return err};report,err:=json.Marshal(prepared.Risk);if err!=nil{return err}
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,`UPDATE order_intents SET request=$2::jsonb,snapshot=$3::jsonb,risk=$4::jsonb,updated_at=now() WHERE id=$1::uuid AND state='prepared'`,id,string(request),string(snapshot),string(report));return oneUpdated(res,err)
}
func(s *Store)MarkSubmitting(ctx context.Context,id string)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return fmt.Errorf("begin submission gate")};defer tx.Rollback()
 var paused bool
 if err=tx.QueryRowContext(ctx,`SELECT a.paused FROM trading_accounts a JOIN order_intents i ON i.account_id=a.account_id WHERE i.id=$1::uuid AND a.anchor_at IS NOT NULL FOR UPDATE OF a`,id).Scan(&paused);err!=nil||paused{return fmt.Errorf("Sandbox trading is paused or uninitialized")}
 res,err:=tx.ExecContext(ctx,`UPDATE order_intents i SET state='submitting',updated_at=now()
 WHERE i.id=$1::uuid AND i.state='prepared' AND i.request IS NOT NULL AND (i.risk->>'allowed')::boolean=true
 AND NOT EXISTS(SELECT 1 FROM order_intents x WHERE x.account_id=i.account_id AND x.id<>i.id AND x.state IN ('submitting','acknowledged','unknown','active','partial'))
 AND NOT EXISTS(SELECT 1 FROM funding_intents f WHERE f.account_id=i.account_id AND f.state NOT IN ('confirmed','abandoned'))`,id)
 if err:=oneUpdated(res,err);err!=nil{return fmt.Errorf("submission gate closed: busy or already submitted")}
 if tx.Commit()!=nil{return fmt.Errorf("submission gate commit failed; inspect intent before retrying")};return nil
}
func(s *Store)SaveAcknowledgement(ctx context.Context,id,brokerID string,raw json.RawMessage)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,`UPDATE order_intents SET state='acknowledged',broker_id=$2,broker_state=$3::jsonb,updated_at=now() WHERE id=$1::uuid AND state='submitting'`,id,brokerID,string(raw));return oneUpdated(res,err)
}
func(s *Store)SaveOrderState(ctx context.Context,id,status string,state tinvest.OrderState,cash,fee int64)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,`UPDATE order_intents SET state=$2,broker_id=$3,broker_state=$4::jsonb,executed_lots=$5,cash_delta_nano=$6,commission_nano=$7,error='',updated_at=now()
 WHERE id=$1::uuid AND state NOT IN ('prepared','blocked') AND executed_lots <= $5`,id,status,state.ID,string(state.Raw),state.LotsExecuted,cash,fee);return oneUpdated(res,err)
}
func(s *Store)MarkOrderError(ctx context.Context,id,status,message string)error{
 if status!="unknown"&&status!="blocked"&&status!="rejected"{return fmt.Errorf("invalid order error state")}
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,`UPDATE order_intents SET state=$2,error=$3,updated_at=now() WHERE id=$1::uuid AND (($2='blocked' AND state='prepared') OR ($2='unknown' AND state NOT IN ('prepared','blocked')) OR ($2='rejected' AND state='submitting'))`,id,status,message);return oneUpdated(res,err)
}
func(s *Store)ListIntents(ctx context.Context,account string,pending bool)([]execution.Intent,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 rows,err:=s.db.QueryContext(ctx,`SELECT row_to_json(r) FROM(SELECT i.*,a.created_at AS analysis_created_at FROM order_intents i JOIN analysis_runs a ON a.id=i.analysis_id WHERE i.account_id=$1 AND (NOT $2::boolean OR i.state IN ('submitting','acknowledged','unknown','active','partial')) ORDER BY i.created_at DESC LIMIT 1000) r`,account,pending)
 if err!=nil{return nil,fmt.Errorf("read Sandbox order intents")};defer rows.Close();result:=[]execution.Intent{}
 for rows.Next(){var data []byte;var i execution.Intent;if rows.Scan(&data)!=nil||json.Unmarshal(data,&i)!=nil{return nil,fmt.Errorf("decode order intent")};result=append(result,i)}
 if rows.Err()!=nil{return nil,fmt.Errorf("read order intents interrupted")};return result,nil
}
func(s *Store)OrderEvents(ctx context.Context,account,id string)(json.RawMessage,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 var data []byte
 err:=s.db.QueryRowContext(ctx,`SELECT coalesce(json_agg(e ORDER BY e.id),'[]'::json) FROM order_events e JOIN order_intents i ON i.id=e.intent_id WHERE i.account_id=$1 AND i.id=$2::uuid`,account,id).Scan(&data)
 if err!=nil{return nil,fmt.Errorf("read order audit events")};return json.RawMessage(data),nil
}


func(s *Store)SaveOrderObservation(ctx context.Context,id string,raw json.RawMessage)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 if !json.Valid(raw){return fmt.Errorf("broker order observation is not JSON")}
 res,err:=s.db.ExecContext(ctx,"UPDATE order_intents SET broker_state=$2::jsonb,updated_at=now() WHERE id=$1::uuid AND state NOT IN ('prepared','blocked')",id,string(raw));return oneUpdated(res,err)
}
