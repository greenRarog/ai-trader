package storage

import (
 "context"
 "encoding/json"
 "fmt"
 "time"
)

type Funding struct{ID string `json:"id"`;AccountID string `json:"account_id"`;Amount int64 `json:"amount_nano"`;Before int64 `json:"before_cash_nano"`;State string `json:"state"`;At time.Time `json:"occurred_at"`;Error string `json:"error"`}
func(s *Store)GetFunding(ctx context.Context,id string)(Funding,error){
 var out Funding;var raw []byte;ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 if s.db.QueryRowContext(ctx,"SELECT row_to_json(f) FROM funding_intents f WHERE id=$1::uuid",id).Scan(&raw)!=nil||json.Unmarshal(raw,&out)!=nil{return out,fmt.Errorf("funding intent not found")};return out,nil
}
func(s *Store)BeginFunding(ctx context.Context,f Funding)error{
 if f.ID==""||f.AccountID==""||f.Amount<=0||f.Before<0||f.At.IsZero(){return fmt.Errorf("invalid funding intent")}
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 // Account advisory lock is held by the caller. Pending mutations block a new
 // non-idempotent SandboxPayIn request even when the previous process crashed.
 tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return fmt.Errorf("begin funding gate")};defer tx.Rollback()
 if _,err=tx.ExecContext(ctx,"INSERT INTO trading_accounts(account_id) VALUES($1) ON CONFLICT DO NOTHING",f.AccountID);err!=nil{return fmt.Errorf("initialize funding gate")}
 var account string
 if err=tx.QueryRowContext(ctx,"SELECT account_id FROM trading_accounts WHERE account_id=$1 FOR UPDATE",f.AccountID).Scan(&account);err!=nil{return fmt.Errorf("lock funding gate")}
 res,err:=tx.ExecContext(ctx,`INSERT INTO funding_intents(id,account_id,amount_nano,before_cash_nano,state,occurred_at)
 SELECT $1::uuid,$2,$3,$4,'funding',$5 WHERE NOT EXISTS(SELECT 1 FROM funding_intents WHERE account_id=$2 AND state NOT IN ('confirmed','abandoned'))
 AND NOT EXISTS(SELECT 1 FROM order_intents WHERE account_id=$2 AND state IN ('submitting','acknowledged','unknown','active','partial'))`,f.ID,f.AccountID,f.Amount,f.Before,f.At)
 if oneUpdated(res,err)!=nil{return fmt.Errorf("persist funding intent failed: unresolved operation or duplicate ID")}
 if tx.Commit()!=nil{return fmt.Errorf("funding gate commit failed; inspect journal before retrying")};return nil
}
func(s *Store)FundingError(ctx context.Context,id,message string)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel();res,err:=s.db.ExecContext(ctx,"UPDATE funding_intents SET state='unknown',error=$2,updated_at=now() WHERE id=$1::uuid AND state IN ('funding','unknown')",id,message);return oneUpdated(res,err)
}
func(s *Store)ConfirmFunding(ctx context.Context,f Funding)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel();tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return fmt.Errorf("begin funding confirmation")};defer tx.Rollback()
 res,err:=tx.ExecContext(ctx,"UPDATE funding_intents SET state='confirmed',error='',updated_at=now() WHERE id=$1::uuid AND account_id=$2 AND state IN ('funding','unknown')",f.ID,f.AccountID)
 if oneUpdated(res,err)!=nil{return fmt.Errorf("funding state changed before confirmation")}
 _,err=tx.ExecContext(ctx,"INSERT INTO cashflows(id,account_id,amount_nano,occurred_at,source,memo) SELECT id,account_id,amount_nano,occurred_at,'sandbox_payin','confirmed SandboxPayIn' FROM funding_intents WHERE id=$1::uuid",f.ID)
 if err!=nil{return fmt.Errorf("save funding cash flow")};if tx.Commit()!=nil{return fmt.Errorf("commit funding confirmation")};return nil
}
func(s *Store)AbandonFunding(ctx context.Context,id string)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,"UPDATE funding_intents SET state='abandoned',error='owner marked as not applied; late credit will cause reconciliation mismatch',updated_at=now() WHERE id=$1::uuid AND state IN ('funding','unknown')",id);return oneUpdated(res,err)
}
func(s *Store)FundingHistory(ctx context.Context,account string)(json.RawMessage,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel();var raw []byte
 if s.db.QueryRowContext(ctx,"SELECT coalesce(json_agg(f ORDER BY f.occurred_at),'[]'::json) FROM funding_intents f WHERE account_id=$1",account).Scan(&raw)!=nil{return nil,fmt.Errorf("read funding journal")};return raw,nil
}
