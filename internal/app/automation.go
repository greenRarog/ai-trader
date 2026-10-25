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

// automation has no order-submission path. Pending intents are queried only;
// funding, cancellation and executing a proposal still require owner commands.
func runAutomation(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)!=1||(args[0]!="tick"&&args[0]!="run"){return fmt.Errorf("use automation tick|run (analysis only)")}
 if c.AccountID==""||c.Token==""{return fmt.Errorf("TINVEST_ACCOUNT_ID and TINVEST_TOKEN are required")}
 keys,err:=candidateKeys(c,strings.Join(c.Preferred,","));if err!=nil{return err}
 settings,err:=config.LoadAnalysis();if err!=nil{return err}
 db,err:=storage.Open(ctx,settings.DatabaseURL);if err!=nil{return err};defer db.Close()
 var model decisionModel=openai.New(settings.APIKey,settings.Timeout)
 if settings.Provider=="codex_cli"{model=codexcli.New(settings.CodexBinary,settings.CodexModel,settings.Timeout)}
 api:=tinvest.New(c.Token,c.Timeout)
 if _,err:=config.LoadTelegram();err!=nil{return err}
 tick:=func()error{
  telegram,err:=config.LoadTelegram();if err!=nil{return err}
  if telegram.Enabled{if err:=flushNotices(ctx,db,c.AccountID,telegram,10);err!=nil{fmt.Fprintln(out,"Warning: notification delivery incomplete; inspect notify list")}}
  err=automationTick(ctx,c,settings,keys,db,api,model,out)
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
func automationTick(ctx context.Context,c config.Config,settings config.Analysis,keys []string,db *storage.Store,api *tinvest.Client,model decisionModel,out io.Writer)error{
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 idle:=func(reason string)error{return printJSON(out,map[string]any{"status":"idle","time":time.Now().UTC(),"reason":reason,"model_attempted":false,"execution_enabled":false})}
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
 now:=time.Now().UTC();day:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,time.UTC)
 calendars,err:=api.Schedules(ctx,"",day,day.Add(48*time.Hour));if err!=nil{return err}
 key,reason:=schedule.Due(now,calendars);if key==""{return idle(reason)}
 claimed,err:=db.ClaimAnalysisSlot(ctx,c.AccountID,key);if err!=nil{return err};if !claimed{return idle("analysis window already attempted; no automatic retry")}
 // Persist the slot before collection or provider calls. Neither failures nor
 // restarts retry this window. Manual analyze --refresh is an explicit override.
 settings.Refresh=false
 return analyze(ctx,c,settings,keys,api,db,model,out)
}

func runUsage(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 fs:=flags("usage");days:=fs.Int("days",7,"Moscow calendar days, including today, 1..365")
 if err:=parse(fs,args);err!=nil{return err};if *days<1||*days>365{return fmt.Errorf("usage --days must be 1..365")}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 data,err:=db.UsageReport(ctx,c.AccountID,*days);if err!=nil{return err}
 return printJSON(out,struct{Rows any `json:"days"`;Note string `json:"note"`}{data,"Provider-reported tokens, including CLI overhead. Missing usage is unknown, not zero cost. Cached tokens are a subset of input. Legacy runs may lack an attempt marker. No currency estimate for ChatGPT subscription usage."})
}
