package storage

import (
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "time"

 "ai-trader/internal/analysis"
)

// ReserveModel is committed before invoking the provider. A crash/timeout still
// consumes the attempt. The account-wide quota spans models and providers.
func(s *Store)ReserveModel(ctx context.Context,id,fingerprint string,refresh bool,limit int,ttl time.Duration)(analysis.Reservation,error){
 var r analysis.Reservation
 if len(fingerprint)!=64||limit<1||ttl<=0||ttl>10*time.Minute{return r,fmt.Errorf("invalid analysis economy settings")}
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return r,fmt.Errorf("begin model reservation")};defer tx.Rollback()
 var account,provider,model,version,status string
 if err=tx.QueryRowContext(ctx,"SELECT account_id,provider,model,prompt_version,status FROM analysis_runs WHERE id=$1::uuid",id).Scan(&account,&provider,&model,&version,&status);err!=nil{return r,fmt.Errorf("analysis not found for reservation")}
 if _,err=tx.ExecContext(ctx,"SELECT pg_advisory_xact_lock(hashtextextended($1,0))","ai-trader-analysis-budget:"+account);err!=nil{return r,fmt.Errorf("lock analysis budget")}
 // Re-read under the budget lock so concurrent reservations of one ID cannot
 // both succeed even when the session-level account lock is not held.
 var attempted bool
 if err=tx.QueryRowContext(ctx,"SELECT status,model_attempted_at IS NOT NULL FROM analysis_runs WHERE id=$1::uuid FOR UPDATE",id).Scan(&status,&attempted);err!=nil||status!="requesting"||attempted{return r,fmt.Errorf("analysis is not eligible for a new model attempt")}
 if !refresh{
  var raw []byte
  err=tx.QueryRowContext(ctx,`SELECT id::text,status,result FROM analysis_runs WHERE account_id=$1 AND provider=$2 AND model=$3 AND prompt_version=$4 AND fingerprint=$5 AND model_attempted_at IS NOT NULL AND model_attempted_at>=now()-($6 * interval '1 second') ORDER BY model_attempted_at DESC LIMIT 1`,account,provider,model,version,fingerprint,ttl.Seconds()).Scan(&r.SourceID,&r.SourceStatus,&raw)
  if err==nil{
   r.Reason="identical context already attempted; no automatic retry"
   if r.SourceStatus=="validated_proposal"{if json.Unmarshal(raw,&r.SourceResult)!=nil{return r,fmt.Errorf("invalid saved model result")};r.Reason="identical context: saved proposal reused"}
   if tx.Commit()!=nil{return r,fmt.Errorf("commit duplicate lookup")};return r,nil
  };if err!=sql.ErrNoRows{return r,fmt.Errorf("read duplicate analysis")}
  var count int
  if err=tx.QueryRowContext(ctx,`SELECT count(*) FROM analysis_runs WHERE account_id=$1 AND model_attempted_at >= (date_trunc('day',now() AT TIME ZONE 'Europe/Moscow') AT TIME ZONE 'Europe/Moscow')`,account).Scan(&count);err!=nil{return r,fmt.Errorf("read daily model budget")}
  if count>=limit{r.Reason="Moscow-day model call limit reached; explicit analyze --refresh is required for an extra attempt";if tx.Commit()!=nil{return r,fmt.Errorf("commit budget lookup")};return r,nil}
 }
 res,err:=tx.ExecContext(ctx,"UPDATE analysis_runs SET fingerprint=$2,model_attempted_at=now() WHERE id=$1::uuid AND status='requesting' AND model_attempted_at IS NULL",id,fingerprint)
 if err=oneUpdated(res,err);err!=nil{return r,err};if tx.Commit()!=nil{return r,fmt.Errorf("commit model reservation; model was not called")}
 r.Attempt=true;return r,nil
}

func(s *Store)ClaimAnalysisSlot(ctx context.Context,account,key string)(bool,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 result,err:=s.db.ExecContext(ctx,"INSERT INTO analysis_slots(account_id,slot_key) VALUES($1,$2) ON CONFLICT DO NOTHING",account,key);if err!=nil{return false,fmt.Errorf("persist daily analysis slot; run db migrate")};n,err:=result.RowsAffected();return n==1,err
}

// UsageReport never invokes a model. Counts include uncertain attempts; reported
// tokens may be incomplete when a provider fails before returning usage.
func(s *Store)UsageReport(ctx context.Context,account string,days int)(json.RawMessage,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 rows,err:=s.db.QueryContext(ctx,`SELECT row_to_json(r) FROM (
 SELECT (coalesce(model_attempted_at,created_at) AT TIME ZONE 'Europe/Moscow')::date AS moscow_day,provider,model,
 count(*) AS analyses,count(*) FILTER(WHERE model_attempted_at IS NOT NULL) AS model_attempts,
 count(*) FILTER(WHERE status='skipped') AS skipped,count(*) FILTER(WHERE status='reused') AS reused,
 count(*) FILTER(WHERE model_attempted_at IS NOT NULL AND result->'usage' IS NOT NULL AND result->'usage'<>'null'::jsonb) AS attempts_with_reported_usage,
 coalesce(sum((result->'usage'->>'input_tokens')::numeric),0)::text AS input_tokens,
 coalesce(sum((result->'usage'->>'output_tokens')::numeric),0)::text AS output_tokens,
 coalesce(sum((result->'usage'->'input_tokens_details'->>'cached_tokens')::numeric),0)::text AS cached_input_tokens,
 coalesce(sum((result->'usage'->>'total_tokens')::numeric),0)::text AS total_tokens,
 coalesce(sum((result->'economy'->>'full_snapshot_bytes')::numeric) FILTER(WHERE model_attempted_at IS NOT NULL),0)::text AS full_snapshot_bytes,
 coalesce(sum((result->'economy'->>'model_input_bytes')::numeric) FILTER(WHERE model_attempted_at IS NOT NULL),0)::text AS model_input_bytes
 FROM analysis_runs WHERE account_id=$1 AND provider IN ('codex_cli','openai_api') AND coalesce(model_attempted_at,created_at) >= ((date_trunc('day',now() AT TIME ZONE 'Europe/Moscow')- (($2-1) * interval '1 day')) AT TIME ZONE 'Europe/Moscow')
 GROUP BY 1,2,3 ORDER BY 1 DESC,2,3) r`,account,days)
 if err!=nil{return nil,fmt.Errorf("read model usage; run db migrate")};defer rows.Close();items:=[]json.RawMessage{}
 for rows.Next(){var data []byte;if rows.Scan(&data)!=nil{return nil,fmt.Errorf("decode model usage")};items=append(items,data)};if rows.Err()!=nil{return nil,fmt.Errorf("model usage interrupted")};return json.Marshal(items)
}
