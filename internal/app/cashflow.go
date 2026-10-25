package app

import (
 "context"
 "fmt"
 "io"
 "strings"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/config"
 "ai-trader/internal/storage"
 "ai-trader/internal/tinvest"
)
func runFunding(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 fs:=flags("account fund");input:=fs.String("amount","","RUB amount to add");confirm:=fs.Bool("confirm",false,"confirm additive Sandbox deposit");id:=fs.String("id","","funding UUID; reuse to inspect same request")
 if err:=parse(fs,args);err!=nil{return err};money,err:=tinvest.ParseDeposit(*input);if err!=nil{return err}
 if !*confirm{return fmt.Errorf("funding adds money on EVERY new ID; pass --confirm")}
 value,err:=amount.Parse(money);if err!=nil{return err};if c.AccountID==""{return fmt.Errorf("set TINVEST_ACCOUNT_ID explicitly")};if c.Token==""{return fmt.Errorf("TINVEST_TOKEN is required")}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close();release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 if *id!=""{
  previous,e:=db.GetFunding(ctx,*id)
  if e==nil{if previous.AccountID!=c.AccountID||previous.Amount!=value{return fmt.Errorf("funding ID belongs to a different request")};return printJSON(out,previous)}
 }else{*id,err=storage.UUID();if err!=nil{return err}}
 api:=tinvest.New(c.Token,c.Timeout);state,err:=accountOnly(ctx,c,api);if err!=nil{return err}
 if state.OpenOrders!=0||len(state.Issues)>0{return fmt.Errorf("funding requires settled, consistent account state")}
 if err:=db.VerifyAccount(ctx,c.AccountID,state,false);err!=nil{return err}
 // Preserve a positive pre-funding baseline. Initial funding of an empty
 // account is seed capital and precedes the first performance observation.
 if state.EquityNano>0{if _,err:=db.RecordEquity(ctx,c.AccountID,"",time.Now().UTC(),state.EquityNano);err!=nil{return err}}
 f:=storage.Funding{ID:*id,AccountID:c.AccountID,Amount:value,Before:state.AvailableRUBNano,At:time.Now().UTC(),State:"funding"}
 if err:=db.BeginFunding(ctx,f);err!=nil{return err}
 if _,err:=fmt.Fprintln(out,"Funding intent:",f.ID);err!=nil{return err}
 balance,err:=api.Fund(ctx,c.AccountID,money)
 if err!=nil{if e:=db.FundingError(context.Background(),f.ID,err.Error());e!=nil{return e};return fmt.Errorf("funding outcome unknown; inspect cashflow funding --id %s; no automatic retry: %w",f.ID,err)}
 expected,e:=amount.Add(f.Before,f.Amount);observed,e2:=amount.Parse(balance)
 if e!=nil||e2!=nil||!strings.EqualFold(balance.Currency,"rub")||observed!=expected{
  _ = db.FundingError(context.Background(),f.ID,"unexpected balance after SandboxPayIn")
  return fmt.Errorf("funding response balance is inconsistent; inspect funding %s",f.ID)
 }
 if err:=db.ConfirmFunding(context.Background(),f);err!=nil{return fmt.Errorf("SandboxPayIn succeeded but journal commit failed; inspect funding %s: %w",f.ID,err)}
 if _,err:=fmt.Fprintln(out,"Virtual deposit recorded. Balance:",balance.String());err!=nil{return err}
 f.State="confirmed";return printJSON(out,f)
}
func signedCashAmount(input string)(int64,error){negative:=strings.HasPrefix(input,"-");if negative{input=strings.TrimPrefix(input,"-")};m,err:=tinvest.ParseDeposit(input);if err!=nil{return 0,err};value,err:=amount.Parse(m);if negative{value=-value};return value,err}
func runCashflow(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)==0{return fmt.Errorf("use cashflow list|fund|funding|record|confirm|abandon")}
 if args[0]=="fund"{return runFunding(ctx,c,args[1:],out)}
 fs:=flags("cashflow "+args[0]);id:=fs.String("id","","operation UUID");input:=fs.String("amount","","signed external RUB amount");at:=fs.String("at","","actual cash-flow RFC3339 timestamp");memo:=fs.String("reason","","owner explanation")
 if err:=parse(fs,args[1:]);err!=nil{return err}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 if args[0]=="list"{data,err:=db.Cashflows(ctx,c.AccountID);if err!=nil{return err};return printJSON(out,data)}
 if args[0]=="funding"{if *id==""{data,err:=db.FundingHistory(ctx,c.AccountID);if err!=nil{return err};return printJSON(out,data)};f,err:=db.GetFunding(ctx,*id);if err!=nil{return err};if f.AccountID!=c.AccountID{return fmt.Errorf("funding belongs to another account")};return printJSON(out,f)}
 if args[0]!="confirm"&&args[0]!="abandon"&&args[0]!="record"{return fmt.Errorf("unknown cashflow command")}
 if *id==""{return fmt.Errorf("--id UUID is required for idempotency")}
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 state,err:=accountOnly(ctx,c,tinvest.New(c.Token,c.Timeout));if err!=nil{return err}
 if state.OpenOrders!=0||len(state.Issues)>0{return fmt.Errorf("account must have no open orders or unsafe positions")}
 if args[0]=="confirm"||args[0]=="abandon"{
  f,err:=db.GetFunding(ctx,*id);if err!=nil{return err};if f.AccountID!=c.AccountID{return fmt.Errorf("wrong funding account")}
  if f.State=="confirmed"||f.State=="abandoned"{return printJSON(out,f)}
  expected:=f.Before
  if args[0]=="confirm"{expected,err=amount.Add(f.Before,f.Amount);if err!=nil{return err}}
  if state.AvailableRUBNano!=expected{return fmt.Errorf("broker cash does not match selected funding outcome")}
  if args[0]=="confirm"{err=db.ConfirmFunding(ctx,f)}else{err=db.AbandonFunding(ctx,f.ID)}
  if err!=nil{return err};updated,err:=db.GetFunding(ctx,f.ID);if err!=nil{return err};return printJSON(out,updated)
 }
 value,err:=signedCashAmount(*input);if err!=nil{return err};when,err:=time.Parse(time.RFC3339,*at);if err!=nil||when.After(time.Now().Add(time.Minute)){return fmt.Errorf("--at must be the actual past RFC3339 cash-flow time")}
 if strings.TrimSpace(*memo)==""{return fmt.Errorf("--reason is required for owner-recorded external cash flow")}
 if previous,found,err:=db.FindCashflow(ctx,*id);err!=nil{return err}else if found{if previous.AccountID!=c.AccountID||previous.Amount!=value{return fmt.Errorf("cash-flow ID conflicts with existing record")};return printJSON(out,previous)}
 cash,holdings,tracked,err:=db.ExpectedAccount(ctx,c.AccountID);if err!=nil{return err};if !tracked{return fmt.Errorf("initialize tracking with trading resume before owner-recorded external adjustments")}
 expected,err:=amount.Add(cash,value);if err!=nil||expected!=state.AvailableRUBNano{return fmt.Errorf("external adjustment does not exactly explain broker cash difference")}
 actual:=map[string]int64{};for _,h:=range state.Holdings{actual[h.UID]=h.Pieces};for uid,n:=range holdings{if actual[uid]!=n{return fmt.Errorf("security mismatch cannot be classified as a cash flow")}};for uid,n:=range actual{if holdings[uid]!=n{return fmt.Errorf("unrecorded security change")}}
 f:=storage.Cashflow{ID:*id,AccountID:c.AccountID,Amount:value,At:when,Source:"owner_record",Memo:*memo}
 if err:=db.RecordCashflow(ctx,f);err!=nil{return err};return printJSON(out,f)
}
