package storage

import (
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "strconv"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
)

type TradingControl struct{AccountID string `json:"account_id"`;Paused bool `json:"paused"`;Reason string `json:"reason"`;Initialized bool `json:"initialized"`}
func(s *Store)TradingControl(ctx context.Context,account string)(TradingControl,error){
 out:=TradingControl{AccountID:account,Paused:true,Reason:"not initialized"}
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 err:=s.db.QueryRowContext(ctx,"SELECT paused,pause_reason,anchor_at IS NOT NULL FROM trading_accounts WHERE account_id=$1",account).Scan(&out.Paused,&out.Reason,&out.Initialized)
 if err==sql.ErrNoRows{return out,nil};if err!=nil{return out,fmt.Errorf("read trading control; run db migrate")};return out,nil
}
func(s *Store)Pause(ctx context.Context,account,reason string)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 _,err:=s.db.ExecContext(ctx,`INSERT INTO trading_accounts(account_id,paused,pause_reason) VALUES($1,true,$2)
 ON CONFLICT(account_id) DO UPDATE SET paused=true,pause_reason=$2,updated_at=now()`,account,reason)
 if err!=nil{return fmt.Errorf("persist trading pause failed")};return nil
}
func(s *Store)InitializeAccount(ctx context.Context,account string,state *analysis.AccountState,when time.Time)error{
 if state==nil||state.OpenOrders!=0||len(state.Issues)>0{return fmt.Errorf("account state is unsafe for initialization")}
 control,err:=s.TradingControl(ctx,account);if err!=nil{return err};if control.Initialized{return nil}
 if len(state.Holdings)!=0||state.AvailableRUBNano!=state.EquityNano||state.EquityNano<=0{return fmt.Errorf("initial trading account must contain only positive RUB cash and no positions")}
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return fmt.Errorf("begin account initialization")};defer tx.Rollback()
 if _,err=tx.ExecContext(ctx,"INSERT INTO trading_accounts(account_id) VALUES($1) ON CONFLICT DO NOTHING",account);err!=nil{return fmt.Errorf("initialize control row")}
 var initialized bool
 if err=tx.QueryRowContext(ctx,"SELECT anchor_at IS NOT NULL FROM trading_accounts WHERE account_id=$1 FOR UPDATE",account).Scan(&initialized);err!=nil{return fmt.Errorf("lock account initialization")}
 if initialized{return tx.Commit()}
 var pending int
 if err=tx.QueryRowContext(ctx,`SELECT (SELECT count(*) FROM funding_intents WHERE account_id=$1 AND state NOT IN ('confirmed','abandoned'))+(SELECT count(*) FROM order_intents WHERE account_id=$1 AND state IN ('submitting','acknowledged','unknown','active','partial'))`,account).Scan(&pending);err!=nil||pending!=0{return fmt.Errorf("resolve pending operations before initializing account tracking")}
 if _,err=tx.ExecContext(ctx,"UPDATE trading_accounts SET paused=true,pause_reason='initialized; paused',anchor_cash_nano=$2,anchor_at=$3,updated_at=now() WHERE account_id=$1 AND anchor_at IS NULL",account,state.AvailableRUBNano,when);err!=nil{return fmt.Errorf("save account anchor")}
 if tx.Commit()!=nil{return fmt.Errorf("commit account anchor")};return nil
}
func(s *Store)Resume(ctx context.Context,account string)error{
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,`UPDATE trading_accounts SET paused=false,pause_reason='',updated_at=now() WHERE account_id=$1 AND anchor_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM funding_intents WHERE account_id=$1 AND state NOT IN ('confirmed','abandoned'))
 AND NOT EXISTS(SELECT 1 FROM order_intents WHERE account_id=$1 AND state IN ('submitting','acknowledged','unknown','active','partial'))`,account)
 if oneUpdated(res,err)!=nil{return fmt.Errorf("cannot resume: initialize/reconcile account and unresolved operations first")};return nil
}
// ExpectedAccount derives cash and shares from immutable funding flows and
// cumulative broker fills. Re-reading the same fill does not add it twice.
func(s *Store)ExpectedAccount(ctx context.Context,account string)(int64,map[string]int64,bool,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 var unsettled int
 if err:=s.db.QueryRowContext(ctx,`SELECT (SELECT count(*) FROM funding_intents WHERE account_id=$1 AND state NOT IN ('confirmed','abandoned'))+(SELECT count(*) FROM order_intents WHERE account_id=$1 AND state IN ('submitting','acknowledged','unknown','active','partial'))`,account).Scan(&unsettled);err!=nil||unsettled!=0{return 0,nil,false,fmt.Errorf("unresolved funding/orders; reconcile before further execution")}
 var anchorText string;var anchorAt time.Time
 err:=s.db.QueryRowContext(ctx,"SELECT anchor_cash_nano::text,anchor_at FROM trading_accounts WHERE account_id=$1 AND anchor_at IS NOT NULL",account).Scan(&anchorText,&anchorAt)
 if err==sql.ErrNoRows{return 0,nil,false,nil};if err!=nil{return 0,nil,false,fmt.Errorf("read account anchor")}
 anchor,err:=strconv.ParseInt(anchorText,10,64);if err!=nil{return 0,nil,false,fmt.Errorf("invalid account anchor")}
 var pending int
 if err=s.db.QueryRowContext(ctx,`SELECT (SELECT count(*) FROM funding_intents WHERE account_id=$1 AND state NOT IN ('confirmed','abandoned'))+(SELECT count(*) FROM order_intents WHERE account_id=$1 AND state IN ('submitting','acknowledged','unknown','active','partial'))`,account).Scan(&pending);err!=nil||pending!=0{return 0,nil,true,fmt.Errorf("unresolved funding/orders; reconcile before further execution")}
 var cashText string
 if err=s.db.QueryRowContext(ctx,`SELECT ($2::numeric + coalesce((SELECT sum(amount_nano) FROM cashflows WHERE account_id=$1 AND occurred_at>$3),0)+coalesce((SELECT sum(cash_delta_nano) FROM order_intents WHERE account_id=$1),0))::text`,account,anchorText,anchorAt).Scan(&cashText);err!=nil{return 0,nil,true,fmt.Errorf("calculate expected cash")}
 cash,err:=strconv.ParseInt(cashText,10,64);if err!=nil{return 0,nil,true,fmt.Errorf("expected cash overflow")};_ = anchor
 rows,err:=s.db.QueryContext(ctx,`SELECT request,snapshot,decision,executed_lots FROM order_intents WHERE account_id=$1 AND executed_lots>0`,account);if err!=nil{return 0,nil,true,fmt.Errorf("read executed quantities")};defer rows.Close()
 holdings:=map[string]int64{}
 for rows.Next(){
  var rawRequest,rawSnapshot,rawDecision []byte;var lots int64
  if rows.Scan(&rawRequest,&rawSnapshot,&rawDecision,&lots)!=nil{return 0,nil,true,fmt.Errorf("read accounting order")}
  var request struct{UID string `json:"instrumentId"`};var snapshot analysis.Snapshot;var decision analysis.Decision
  if json.Unmarshal(rawRequest,&request)!=nil||json.Unmarshal(rawSnapshot,&snapshot)!=nil||json.Unmarshal(rawDecision,&decision)!=nil{return 0,nil,true,fmt.Errorf("invalid accounting order")}
  var lot int64
  for _,q:=range snapshot.Quotes{if q.Instrument.UID==request.UID{lot=int64(q.Instrument.Lot)}}
  if lot<=0||request.UID==""{return 0,nil,true,fmt.Errorf("accounting order has no lot metadata")}
  pieces,e:=amount.Mul(lots,lot);if e!=nil{return 0,nil,true,e};if decision.Action=="SELL"{pieces=-pieces}
  holdings[request.UID],e=amount.Add(holdings[request.UID],pieces);if e!=nil{return 0,nil,true,e}
 }
 if rows.Err()!=nil{return 0,nil,true,fmt.Errorf("accounting orders interrupted")};return cash,holdings,true,nil
}
func(s *Store)VerifyAccount(ctx context.Context,account string,state *analysis.AccountState,strict bool)error{
 cash,holdings,tracked,err:=s.ExpectedAccount(ctx,account);if err!=nil{return err}
 if !tracked{if strict{return fmt.Errorf("trading account is not initialized; use trading resume")};return nil}
 if state==nil||len(state.Issues)>0||state.OpenOrders!=0{return fmt.Errorf("broker account has unresolved orders or unsafe positions")}
 if cash!=state.AvailableRUBNano{return fmt.Errorf("cash mismatch: ledger=%s, broker=%s nanorubles; classify external cash flow or reconcile fills",amount.Text(cash),amount.Text(state.AvailableRUBNano))}
 actual:=map[string]int64{};for _,h:=range state.Holdings{actual[h.UID]=h.Pieces}
 for uid,n:=range holdings{if actual[uid]!=n{return fmt.Errorf("broker positions differ from recorded executions")}}
 for uid,n:=range actual{if holdings[uid]!=n{return fmt.Errorf("unrecorded external security position")}}
 return nil
}
type Cashflow struct{ID string `json:"id"`;AccountID string `json:"account_id"`;Amount int64 `json:"amount_nano"`;At time.Time `json:"occurred_at"`;Source string `json:"source"`;Memo string `json:"memo"`}
func(s *Store)RecordCashflow(ctx context.Context,f Cashflow)error{
 if f.Source=="sandbox_payin"&&f.Amount<=0{return fmt.Errorf("SandboxPayIn cash flow must be positive")}
 if f.Amount==0||f.ID==""||f.AccountID==""||f.At.IsZero()||(f.Source!="owner_record"&&f.Source!="sandbox_payin"){return fmt.Errorf("invalid cash flow")}
 f.At=f.At.UTC().Truncate(time.Microsecond)
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 if f.Source=="owner_record"{
  var anchor time.Time
  if err:=s.db.QueryRowContext(ctx,"SELECT anchor_at FROM trading_accounts WHERE account_id=$1 AND anchor_at IS NOT NULL",f.AccountID).Scan(&anchor);err!=nil||!f.At.After(anchor){return fmt.Errorf("owner-recorded flow must occur after the account tracking anchor")}
 }
 res,err:=s.db.ExecContext(ctx,`INSERT INTO cashflows(id,account_id,amount_nano,occurred_at,source,memo) VALUES($1::uuid,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`,f.ID,f.AccountID,f.Amount,f.At,f.Source,f.Memo)
 if err!=nil{return fmt.Errorf("save external cash flow")};n,err:=res.RowsAffected();if err!=nil{return err};if n==1{return nil}
 var account,source,memo string;var value int64;var at time.Time
 if err=s.db.QueryRowContext(ctx,"SELECT account_id,amount_nano::bigint,source,memo,occurred_at FROM cashflows WHERE id=$1::uuid",f.ID).Scan(&account,&value,&source,&memo,&at);err!=nil||account!=f.AccountID||value!=f.Amount||source!=f.Source||memo!=f.Memo||!at.Equal(f.At){return fmt.Errorf("cash-flow ID was already used with different parameters")};return nil
}
func(s *Store)Cashflows(ctx context.Context,account string)(json.RawMessage,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel();var data []byte
 err:=s.db.QueryRowContext(ctx,`SELECT coalesce(json_agg(f ORDER BY f.occurred_at,f.id),'[]'::json) FROM cashflows f WHERE account_id=$1`,account).Scan(&data)
 if err!=nil{return nil,fmt.Errorf("read cash-flow journal")};return data,nil
}

func(s *Store)FindCashflow(ctx context.Context,id string)(Cashflow,bool,error){
 var out Cashflow;var raw []byte;ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 err:=s.db.QueryRowContext(ctx,"SELECT row_to_json(f) FROM cashflows f WHERE id=$1::uuid",id).Scan(&raw)
 if err==sql.ErrNoRows{return out,false,nil};if err!=nil{return out,false,fmt.Errorf("read cash-flow UUID")};if json.Unmarshal(raw,&out)!=nil{return out,false,fmt.Errorf("decode cash flow")};return out,true,nil
}
