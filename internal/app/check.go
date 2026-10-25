package app

import (
 "context"
 "encoding/json"
 "fmt"
 "io"
 "strings"
 "time"

 "ai-trader/internal/analysis"
 "ai-trader/internal/config"
 "ai-trader/internal/storage"
 "ai-trader/internal/tinvest"
)

type manualModel struct{ decision analysis.Decision }
func (m manualModel) Respond(context.Context,json.RawMessage)(analysis.Result,error){
 raw,_:=json.Marshal(m.decision);return analysis.Result{Decision:&m.decision,Raw:raw,Model:"owner-proposal"},nil
}
func runRiskCheck(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 fs:=flags("risk-check");action:=fs.String("action","","BUY, SELL or HOLD");instrument:=fs.String("instrument","","TICKER:CLASS");lots:=fs.Int64("lots",0,"number of whole lots")
 if err:=parse(fs,args);err!=nil{return err}
 d:=analysis.Decision{Action:strings.ToUpper(*action),Lots:*lots,Reason:"Предложение владельца для проверки Risk Engine; без исполнения"}
 input:=*instrument
 if d.Action=="HOLD"{if input!=""||*lots!=0{return fmt.Errorf("HOLD requires no instrument and zero lots")};input=strings.Join(c.Preferred,",")}else{
  pair:=strings.Split(strings.ToUpper(strings.TrimSpace(input)),":")
  if (d.Action!="BUY"&&d.Action!="SELL")||len(pair)!=2||pair[0]==""||pair[1]==""||*lots<=0{return fmt.Errorf("use risk-check --action BUY|SELL --instrument TICKER:CLASS --lots N")}
  d.Ticker,d.ClassCode=pair[0],pair[1]
 }
 keys,err:=candidateKeys(c,input);if err!=nil{return err}
 if c.Token==""||c.AccountID==""{return fmt.Errorf("TINVEST_TOKEN and TINVEST_ACCOUNT_ID are required")}
 dsn,err:=config.DatabaseURL();if err!=nil{return err};db,err:=storage.Open(ctx,dsn);if err!=nil{return err};defer db.Close()
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 return analyze(ctx,c,config.Analysis{Provider:"manual_check",Model:"owner-proposal"},keys,tinvest.New(c.Token,c.Timeout),db,manualModel{d},out)
}
func runMarket(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 fs:=flags("market");compact:=fs.Bool("compact",false,"inspect compact model context without invoking AI");input:=fs.String("instruments",strings.Join(c.Preferred,","),"TICKER:CLASS candidates")
 if err:=parse(fs,args);err!=nil{return err};keys,err:=candidateKeys(c,*input);if err!=nil{return err}
 if c.Token==""||c.AccountID==""{return fmt.Errorf("TINVEST_TOKEN and TINVEST_ACCOUNT_ID are required")}
 snapshot,err:=collectSnapshot(ctx,c,keys,tinvest.New(c.Token,c.Timeout));if err!=nil{return err}
 if *compact{
  modelInput,err:=analysis.Compact(snapshot);if err!=nil{return err};full,_:=json.Marshal(snapshot);small,_:=json.Marshal(modelInput)
  return printJSON(out,map[string]any{"model_attempted":false,"full_snapshot_bytes":len(full),"model_input_bytes":len(small),"context":modelInput,"note":"Inspection only, before local filtering. Equity history is added by analyze. No model called."})
 };return printJSON(out,snapshot)
}
func runEquity(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)==0{return fmt.Errorf("use equity sample or equity history [--limit 20]")}
 if c.AccountID==""{return fmt.Errorf("TINVEST_ACCOUNT_ID is required")}
 limit:=20
 switch args[0]{case "sample":if len(args)!=1{return fmt.Errorf("equity sample accepts no arguments")}
 case "history":fs:=flags("equity history");fs.IntVar(&limit,"limit",20,"number of observations");if err:=parse(fs,args[1:]);err!=nil{return err};if limit<1||limit>1000{return fmt.Errorf("limit must be 1..1000")}
 default:return fmt.Errorf("use equity sample or equity history")}
 dsn,err:=config.DatabaseURL();if err!=nil{return err};db,err:=storage.Open(ctx,dsn);if err!=nil{return err};defer db.Close()
 if args[0]=="history"{data,err:=db.EquityHistory(ctx,c.AccountID,limit);if err!=nil{return err};return printJSON(out,data)}
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
 if c.Token==""{return fmt.Errorf("TINVEST_TOKEN is required")}
 api:=tinvest.New(c.Token,c.Timeout);if err:=checkAccount(ctx,api,c.AccountID);err!=nil{return err}
 observed:=time.Now().UTC();positions,err:=api.Snapshot(ctx,c.AccountID,false);if err!=nil{return err};portfolio,err:=api.Snapshot(ctx,c.AccountID,true);if err!=nil{return err}
 orders,err:=api.OpenOrderCount(ctx,c.AccountID);if err!=nil{return err}
 account,err:=analysis.DecodeAccount(positions,portfolio,orders);if err!=nil{return err}
 if err:=db.VerifyAccount(ctx,c.AccountID,account,false);err!=nil{return err}
 metrics,err:=db.RecordEquity(ctx,c.AccountID,"",observed,account.EquityNano);if err!=nil{return err};return printJSON(out,metrics)
}
