package app

import (
 "context"
 "fmt"
 "io"
 "strings"
 "time"

 "ai-trader/internal/codexcli"
 "ai-trader/internal/config"
 "ai-trader/internal/execution"
 "ai-trader/internal/openai"
 "ai-trader/internal/schedule"
 "ai-trader/internal/storage"
 "ai-trader/internal/tinvest"
)

// Automatic execution is opt-in and uses only this scheduler's durable analyses.
func runAutomation(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)==0||(args[0]!="tick"&&args[0]!="run"){return fmt.Errorf("use automation tick [--now]|run")}
 fs:=flags("automation "+args[0]);now:=fs.Bool("now",false,"explicit immediate analysis; keeps daily model quota and risk gates")
 if err:=parse(fs,args[1:]);err!=nil{return err};if *now&&args[0]!="tick"{return fmt.Errorf("--now requires automation tick")}
 if c.AccountID==""||c.Token==""{return fmt.Errorf("TINVEST_ACCOUNT_ID and TINVEST_TOKEN are required")}
 keys,err:=candidateKeys(c,strings.Join(c.Preferred,","));if err!=nil{return err}
 settings,err:=config.LoadAnalysis();if err!=nil{return err}
 db,err:=storage.Open(ctx,settings.DatabaseURL);if err!=nil{return err};defer db.Close()
 var model decisionModel=openai.New(settings.APIKey,settings.Timeout)
 if settings.Provider=="codex_cli"{model=codexcli.New(settings.CodexBinary,settings.CodexModel,settings.Timeout)}
 api:=tinvest.New(c.Token,c.Timeout)
 if _,err:=config.LoadTelegram();err!=nil{return err}
 if err:=printJSON(out,map[string]any{"status":"automation_started","execution_enabled":settings.AutoExecute,"provider":settings.Provider,"daily_model_limit":settings.DailyLimit,"instrument_mode":c.Mode,"instruments":keys,"time":time.Now().UTC()});err!=nil{return err}
 tick:=func()error{
  telegram,err:=config.LoadTelegram();if err!=nil{return err}
  if telegram.Enabled{if err:=flushNotices(ctx,db,c.AccountID,telegram,10);err!=nil{fmt.Fprintln(out,"Warning: notification delivery incomplete; inspect notify list")}}
  err=automationTickNow(ctx,c,settings,keys,db,api,model,out,*now)
  if err!=nil&&ctx.Err()==nil{queueNotice(ctx,db,c.AccountID,"cycle_error:"+time.Now().UTC().Format("2006-01-02T15"),"Ошибка автономного цикла. Проверь локальный журнал и trading status.",out)}
  return err
 }
 if args[0]=="tick"{return tick()}
 ticker:=time.NewTicker(time.Minute);defer ticker.Stop()
 for{
  if err:=tick();err!=nil{if ctx.Err()!=nil{return ctx.Err()};if err:=printJSON(out,map[string]any{"status":"cycle_error","time":time.Now().UTC(),"error":err.Error()});err!=nil{return err}}
  select{case <-ctx.Done():return ctx.Err();case <-ticker.C:}
 }
}
func automationTick(ctx context.Context,c config.Config,settings config.Analysis,keys []string,db *storage.Store,api tradingBroker,model decisionModel,out io.Writer)error{
 return automationTickNow(ctx,c,settings,keys,db,api,model,out,false)
}
func automationTickNow(ctx context.Context,c config.Config,settings config.Analysis,keys []string,db *storage.Store,api tradingBroker,model decisionModel,out io.Writer,immediate bool)error{
 idle:=func(reason string)error{return printJSON(out,map[string]any{"status":"idle","time":time.Now().UTC(),"reason":reason,"model_attempted":false,"execution_enabled":settings.AutoExecute})}
 // Check before any broker/model request, including reconciliation and --now.
 if !settings.Hours.Allows(time.Now().UTC()){return idle("outside configured Moscow working hours")}
 if settings.Hours.Enabled{
  _,end:=settings.Hours.Bounds(time.Now().UTC())
  var cancel context.CancelFunc
  ctx,cancel=context.WithDeadline(ctx,end);defer cancel()
 }
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 control,err:=db.TradingControl(ctx,c.AccountID);if err!=nil{return err}
 // Recover pending broker states even while paused, without resubmitting them.
 pending,err:=db.ListIntents(ctx,c.AccountID,true);if err!=nil{return err}
 for _,intent:=range pending{updated,err:=execution.Reconcile(ctx,db,api,intent.ID);if err!=nil{_ = db.Pause(context.Background(),c.AccountID,"automation: unresolved Sandbox order");return err};orderNotice(ctx,db,c.AccountID,updated,out);if !execution.Terminal(updated.State){return idle("Sandbox order remains open; no model call")}}
 if control.Initialized{
  state,err:=accountOnly(ctx,c,api);if err!=nil{return err}
  if err:=db.VerifyAccount(ctx,c.AccountID,state,true);err!=nil{_ = db.Pause(context.Background(),c.AccountID,"automation: account ledger mismatch");return err}
  if len(state.Holdings)>0||len(pending)>0{
  equity,err:=db.RecordEquity(ctx,c.AccountID,"",time.Now().UTC(),state.EquityNano);if err!=nil{return err}
  if equity.DailyHalt||equity.DrawdownHalt{if err:=db.Pause(ctx,c.AccountID,"automation: sampled loss limit reached");err!=nil{return err};queueNotice(ctx,db,c.AccountID,"loss_stop:"+time.Now().UTC().Format("2006-01-02"),"Наблюдаемый лимит потерь достигнут. Счёт поставлен на паузу; принудительной продажи нет.",out);return idle("sampled loss limit reached; account paused; no forced sale")}
  }
 }
 // Re-read so an emergency owner pause during recovery always wins.
 control,err=db.TradingControl(ctx,c.AccountID);if err!=nil{return err};if control.Paused||!control.Initialized{return idle("trading paused or uninitialized")}
 // Recover validated/prepared automatic work before checking the next window.
 if settings.AutoExecute{did,err:=executeAutomatic(ctx,c,db,api,out);if err!=nil{return err};if did{return nil}}
 now:=time.Now().UTC();day:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,time.UTC)
 calendars,err:=api.Schedules(ctx,"",day,day.Add(48*time.Hour));if err!=nil{return err}
 key,reason:=schedule.DueWithin(now,calendars,settings.Hours)
 if immediate{key="explicit:"+now.Format(time.RFC3339Nano)}else if key==""{return idle(reason)}
 claimed,err:=db.ClaimAnalysisSlot(ctx,c.AccountID,key);if err!=nil{return err};if !claimed{return idle("analysis window already attempted; no automatic retry")}
 // Persist the slot before collection or provider calls. Neither failures nor
 // restarts retry this window. Manual analyze --refresh is an explicit override.
 settings.Refresh=false
 settings.Automatic=settings.AutoExecute
 if err:=analyze(ctx,c,settings,keys,api,db,model,out);err!=nil{return err}
 if settings.AutoExecute{_,err:=executeAutomatic(ctx,c,db,api,out);return err}
 return nil
}

// Caller holds the account lock. No nested CLI invocation or second lock.
func executeAutomatic(ctx context.Context,c config.Config,db *storage.Store,api tradingBroker,out io.Writer)(bool,error){
 ids,err:=db.AutomaticCandidates(ctx,c.AccountID);if err!=nil||len(ids)==0{return false,err}
 for _,id:=range ids{
  control,err:=db.TradingControl(ctx,c.AccountID);if err!=nil{return true,err}
  if control.Paused||!control.Initialized{return true,fmt.Errorf("automatic execution paused/uninitialized")}
  intent,err:=db.CreateIntent(ctx,c.AccountID,id);if err!=nil{return true,err}
  result,err:=execution.Perform(ctx,db,api,intent.ID,func(ctx context.Context,i execution.Intent)(execution.Prepared,error){return preflightOrder(ctx,c,db,api,i)})
  if err!=nil{
   latest,readErr:=db.GetIntent(context.WithoutCancel(ctx),intent.ID)
   if readErr!=nil||!execution.Terminal(latest.State){_ = db.Pause(context.WithoutCancel(ctx),c.AccountID,"automation: order requires reconciliation")}
   if readErr==nil{orderNotice(ctx,db,c.AccountID,latest,out)}
   return true,err
  }
  orderNotice(ctx,db,c.AccountID,result,out)
  if err:=printJSON(out,map[string]any{"status":"automatic_order","execution_enabled":true,"analysis_id":id,"intent_id":result.ID,"state":result.State,"action":result.Decision.Action,"ticker":result.Decision.Ticker,"executed_lots":result.ExecutedLots});err!=nil{return true,err}
  if !execution.Terminal(result.State){return true,nil}
  if err:=verifyAfterExecution(ctx,c,db,api);err!=nil{return true,err}
 }
 return true,nil
}

func runUsage(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 fs:=flags("usage");days:=fs.Int("days",7,"Moscow calendar days, including today, 1..365")
 if err:=parse(fs,args);err!=nil{return err};if *days<1||*days>365{return fmt.Errorf("usage --days must be 1..365")}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 data,err:=db.UsageReport(ctx,c.AccountID,*days);if err!=nil{return err}
 return printJSON(out,struct{Rows any `json:"days"`;Note string `json:"note"`}{data,"Provider-reported tokens, including CLI overhead. Missing usage is unknown, not zero cost. Cached tokens are a subset of input. Legacy runs may lack an attempt marker. No currency estimate for ChatGPT subscription usage."})
}
