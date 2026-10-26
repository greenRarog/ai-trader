package app

import (
 "context"
 "fmt"
 "io"
 "strings"
 "time"

 "ai-trader/internal/analysis"
 "ai-trader/internal/config"
 "ai-trader/internal/execution"
 "ai-trader/internal/risk"
 "ai-trader/internal/storage"
 "ai-trader/internal/tinvest"
)

func tradingDB(ctx context.Context,c config.Config)(*storage.Store,error){
 if c.AccountID==""{return nil,fmt.Errorf("TINVEST_ACCOUNT_ID is required")};dsn,err:=config.DatabaseURL();if err!=nil{return nil,err};return storage.Open(ctx,dsn)
}
type tradingBroker interface { analysisBroker; execution.Broker }
func accountOnly(ctx context.Context,c config.Config,api analysisBroker)(*analysis.AccountState,error){
 if c.Token==""{return nil,fmt.Errorf("TINVEST_TOKEN is required")};if err:=checkAccount(ctx,api,c.AccountID);err!=nil{return nil,err}
 positions,err:=api.Snapshot(ctx,c.AccountID,false);if err!=nil{return nil,err};portfolio,err:=api.Snapshot(ctx,c.AccountID,true);if err!=nil{return nil,err};orders,err:=api.OpenOrderCount(ctx,c.AccountID);if err!=nil{return nil,err};return analysis.DecodeAccount(positions,portfolio,orders)
}
func runTrading(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)!=1||(args[0]!="status"&&args[0]!="pause"&&args[0]!="resume"){return fmt.Errorf("use trading status|pause|resume")}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 if args[0]=="pause"{if err:=db.Pause(ctx,c.AccountID,"owner pause");err!=nil{return err}}else if args[0]=="resume"{
  release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
  state,err:=accountOnly(ctx,c,tinvest.New(c.Token,c.Timeout));if err!=nil{return err}
  if err:=db.InitializeAccount(ctx,c.AccountID,state,time.Now().UTC());err!=nil{return err}
  if err:=db.VerifyAccount(ctx,c.AccountID,state,true);err!=nil{return err}
  if _,err:=db.RecordEquity(ctx,c.AccountID,"",time.Now().UTC(),state.EquityNano);err!=nil{return err}
  if err:=db.Resume(ctx,c.AccountID);err!=nil{return err}
 }
 control,err:=db.TradingControl(ctx,c.AccountID);if err!=nil{return err}
 if args[0]!="status"{id,_:=storage.UUID();queueNotice(ctx,db,c.AccountID,"control:"+id,fmt.Sprintf("Управление счётом: %s · paused=%t",args[0],control.Paused),out)}
 return printJSON(out,control)
}
func runOrders(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)==0{return fmt.Errorf("use orders list|execute|reconcile|cancel|events")}
 fs:=flags("orders "+args[0]);id:=fs.String("id","","persisted intent UUID");analysisID:=fs.String("analysis","","saved AI analysis UUID")
 sandboxTest:=fs.Bool("sandbox-test",false,"explicitly execute a one-lot owner risk-check in Sandbox")
 if err:=parse(fs,args[1:]);err!=nil{return err}
 if *sandboxTest&&args[0]!="execute"{return fmt.Errorf("--sandbox-test is only valid with orders execute")}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 if args[0]=="list"{if *id!=""{intent,err:=db.GetIntent(ctx,*id);if err!=nil{return err};if intent.AccountID!=c.AccountID{return fmt.Errorf("intent belongs to another account")};return printJSON(out,intent)};items,err:=db.ListIntents(ctx,c.AccountID,false);if err!=nil{return err};return printJSON(out,items)}
 if args[0]=="events"{if *id==""{return fmt.Errorf("--id is required")};events,err:=db.OrderEvents(ctx,c.AccountID,*id);if err!=nil{return err};return printJSON(out,events)}
 if c.Token==""{return fmt.Errorf("TINVEST_TOKEN is required")}
 if args[0]!="execute"&&args[0]!="reconcile"&&args[0]!="cancel"{return fmt.Errorf("unknown orders command")}
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 api:=tinvest.New(c.Token,c.Timeout)
 if err:=checkAccount(ctx,api,c.AccountID);err!=nil{return err}
 if args[0]=="reconcile"&&*id==""{
  items,err:=db.ListIntents(ctx,c.AccountID,true);if err!=nil{return err}
  results:=[]execution.Intent{};var firstError error
  for _,item:=range items{updated,e:=execution.Reconcile(ctx,db,api,item.ID);if e!=nil&&firstError==nil{firstError=e};if e==nil{orderNotice(ctx,db,c.AccountID,updated,out)};results=append(results,updated)}
  if err:=printJSON(out,results);err!=nil{return err}
  if firstError!=nil{_ = db.Pause(context.Background(),c.AccountID,"unresolved Sandbox order");return firstError}
  return verifyAfterExecution(ctx,c,db,api)
 }
 if args[0]=="execute"{
  if *analysisID==""{return fmt.Errorf("orders execute requires --analysis UUID")}
  var intent execution.Intent
  var err error
  if *sandboxTest{intent,err=db.CreateSandboxTestIntent(ctx,c.AccountID,*analysisID)}else{intent,err=db.CreateIntent(ctx,c.AccountID,*analysisID)}
  if err!=nil{return err};*id=intent.ID
 }else if *id==""{return fmt.Errorf("--id is required")}
 intent,err:=db.GetIntent(ctx,*id);if err!=nil{return err};if intent.AccountID!=c.AccountID{return fmt.Errorf("intent belongs to another Sandbox account")}
 if _,err:=fmt.Fprintln(out,"Sandbox order intent:",intent.ID);err!=nil{return err}
 var result execution.Intent
 switch args[0]{
 case "execute":result,err=execution.Perform(ctx,db,api,intent.ID,func(ctx context.Context,intent execution.Intent)(execution.Prepared,error){return preflightOrder(ctx,c,db,api,intent)})
 case "reconcile":result,err=execution.Reconcile(ctx,db,api,intent.ID)
 case "cancel":result,err=execution.Cancel(ctx,db,api,intent.ID)
 }
 if err!=nil{
  // Ambiguous outcomes persist and close the account gate. A blocked preflight
  // has not sent an order, so it does not by itself require pausing the account.
  latest,readErr:=db.GetIntent(context.Background(),intent.ID)
  if readErr==nil{orderNotice(ctx,db,c.AccountID,latest,out)}
  if readErr!=nil||(!execution.Terminal(latest.State)){_ = db.Pause(context.Background(),c.AccountID,"order requires reconciliation")}
  return fmt.Errorf("intent %s: %w",intent.ID,err)
 }
 orderNotice(ctx,db,c.AccountID,result,out)
 if err:=printJSON(out,result);err!=nil{return err}
 if result.State=="blocked"{return fmt.Errorf("intent was blocked and will not be resubmitted; use a new analysis")}
 if execution.Terminal(result.State){return verifyAfterExecution(ctx,c,db,api)}
 return nil
}
func preflightOrder(ctx context.Context,c config.Config,db *storage.Store,api analysisBroker,intent execution.Intent)(execution.Prepared,error){
 var prepared execution.Prepared
 control,err:=db.TradingControl(ctx,c.AccountID);if err!=nil{return prepared,err};if control.Paused||!control.Initialized{return prepared,fmt.Errorf("Sandbox trading paused/uninitialized; use trading resume")}
 if intent.AnalysisCreatedAt.IsZero()||time.Since(intent.AnalysisCreatedAt)>execution.MaxAnalysisAge{return prepared,fmt.Errorf("intent is older than 10 minutes; run a new analyze")}
 keys,err:=candidateKeys(c,intent.Decision.Ticker+":"+intent.Decision.ClassCode);if err!=nil{return prepared,err}
 snapshot,err:=collectSnapshot(ctx,c,keys,api);if err!=nil{return prepared,err}
 if err:=db.VerifyAccount(ctx,c.AccountID,snapshot.Account,true);err!=nil{_ = db.Pause(context.Background(),c.AccountID,"account ledger mismatch");return prepared,err}
 metrics,err:=db.RecordEquity(ctx,c.AccountID,"",snapshot.CapturedAt,snapshot.Account.EquityNano);if err!=nil{return prepared,err};snapshot.Equity=&metrics
 report:=risk.Check(intent.Decision,snapshot,time.Now().UTC(),risk.Options{QuoteMaxAge:c.QuoteMaxAge,MarketMaxAge:c.MarketMaxAge,SlippageBPS:c.SlippageBPS,LimitOrders:true})
 if !report.Allowed{return prepared,fmt.Errorf("fresh risk check: %s",strings.Join(report.Reasons,"; "))}
 request,err:=execution.BuildLimit(intent,snapshot,report);if err!=nil{return prepared,err}
 // Refresh narrow state/status/book again through a full preflight snapshot is
 // intentionally not duplicated: no model call occurs between this check and
 // the durable submitting transition. The limit price bounds subsequent drift.
 return execution.Prepared{Request:request,Snapshot:snapshot,Risk:report},nil
}
func verifyAfterExecution(ctx context.Context,c config.Config,db *storage.Store,api analysisBroker)error{
 state,err:=accountOnly(ctx,c,api);if err!=nil{_ = db.Pause(context.Background(),c.AccountID,"post-execution account read failed");return err}
 if err:=db.VerifyAccount(ctx,c.AccountID,state,true);err!=nil{_ = db.Pause(context.Background(),c.AccountID,"post-execution ledger mismatch");return err}
 _,err=db.RecordEquity(ctx,c.AccountID,"",time.Now().UTC(),state.EquityNano);return err
}
