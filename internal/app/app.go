package app

import (
 "context"
 "encoding/json"
 "flag"
 "fmt"
 "io"
 "math/big"
 "strings"
 "time"

 "ai-trader/internal/config"
 "ai-trader/internal/policy"
 "ai-trader/internal/tinvest"
)

const Help = `AI Trader — Sandbox only, analysis + explicitly invoked Sandbox execution
Commands:
  db migrate | db ping
  analyze [--instruments SBER:TQBR,GAZP:TQBR] [--refresh]
  automation tick | automation run (analysis only; two daily windows)
  usage [--days 7]
  report [--days 30]
  notify test | notify flush | notify list
  history [--limit 20] [--id UUID]
  market [--instruments SBER:TQBR,GAZP:TQBR] [--compact]
  equity sample | equity history [--limit 20]
  risk-check --action BUY|SELL --instrument SBER:TQBR --lots 1
  trading status | trading pause | trading resume
  orders execute --analysis UUID
  orders list [--id UUID] | orders events --id UUID
  orders reconcile [--id UUID] | orders cancel --id UUID
  cashflow list | cashflow funding [--id UUID]
  cashflow record --id UUID --amount RUB --at RFC3339 --reason TEXT
  cashflow confirm --id UUID | cashflow abandon --id UUID
  accounts
  account create --name ai-trader
  account fund --amount 100000 --confirm [--id UUID]
  balance
  quotes [--instruments SBER:TQBR,GAZP:TQBR]

analyze --refresh bypasses cache/daily quota, but never data or safety gates.
analyze and automation never submit orders. orders execute places Sandbox limit orders only.
AI_PROVIDER: codex_cli (host launcher) | openai_api.
INSTRUMENT_MODE: protective (allowlist) | experimental (list ignored).
Mutations are never retried automatically. Funding is ADDITIVE on every invocation.
TINVEST_ACCOUNT_ID must be explicitly configured for balance and funding.
`
func Run(ctx context.Context,c config.Config,args []string,out io.Writer) error {
 if len(args)==0 { return fmt.Errorf("command required") }
 switch args[0] {
 case "automation": return runAutomation(ctx,c,args[1:],out)
 case "report": return runReport(ctx,c,args[1:],out)
 case "notify": return runNotify(ctx,c,args[1:],out)
 case "usage": return runUsage(ctx,c,args[1:],out)
 case "trading": return runTrading(ctx,c,args[1:],out)
 case "orders": return runOrders(ctx,c,args[1:],out)
 case "cashflow": return runCashflow(ctx,c,args[1:],out)
 case "market": return runMarket(ctx,c,args[1:],out)
 case "equity": return runEquity(ctx,c,args[1:],out)
 case "risk-check": return runRiskCheck(ctx,c,args[1:],out)
 case "db": return runDatabase(ctx,args[1:],out)
 case "history": return runHistory(ctx,args[1:],out)
 case "analyze": return runAnalyze(ctx,c,args[1:],out)
 }
 if c.Token=="" { return fmt.Errorf("TINVEST_TOKEN is required") }
 api:=tinvest.New(c.Token,c.Timeout)
 switch args[0] {
 case "accounts":
  if len(args)!=1 { return fmt.Errorf("accounts accepts no arguments") }
  accounts,err:=api.Accounts(ctx); if err!=nil { return err }; return printJSON(out,accounts)
 case "account":
  if len(args)<2 { return fmt.Errorf("expected account create or account fund") }
  switch args[1] {
  case "create":
   fs:=flags("create"); name:=fs.String("name","ai-trader","Sandbox account name")
   if err:=parse(fs,args[2:]); err!=nil { return err }
   id,err:=api.Create(ctx,*name); if err!=nil { return fmt.Errorf("creation outcome may be unknown; inspect accounts before retrying: %w",err) }
   _,err=fmt.Fprintf(out,"Created Sandbox account. Set TINVEST_ACCOUNT_ID=%s in .env\n",id); return err
  case "fund": return runFunding(ctx,c,args[2:],out)
  default: return fmt.Errorf("unknown account command")
  }
 case "balance":
  if len(args)!=1 { return fmt.Errorf("balance accepts no arguments") }
  if err:=checkAccount(ctx,api,c.AccountID); err!=nil { return err }
  positions,err:=api.Snapshot(ctx,c.AccountID,false); if err!=nil { return err }
  portfolio,err:=api.Snapshot(ctx,c.AccountID,true); if err!=nil { return err }
  return printJSON(out,struct { Positions json.RawMessage `json:"positions"`; Portfolio json.RawMessage `json:"portfolio"` }{positions,portfolio})
 case "quotes":
  fs:=flags("quotes"); instruments:=fs.String("instruments",strings.Join(c.Preferred,","),"comma-separated TICKER:CLASS")
  if err:=parse(fs,args[1:]); err!=nil { return err }
  if strings.TrimSpace(*instruments)=="" { return fmt.Errorf("specify instruments explicitly or configure PREFERRED_INSTRUMENTS") }
  metadata:=[]tinvest.Instrument{}; ids:=[]string{}; seen:=map[string]bool{}
  for _,key:=range strings.Split(*instruments,",") {
   key=strings.ToUpper(strings.TrimSpace(key)); pair:=strings.Split(key,":")
   if len(pair)!=2 || pair[0]=="" || pair[1]=="" { return fmt.Errorf("use TICKER:CLASS") }
   // Reject disallowed symbols before sending requests in protective mode.
   if err:=policy.Check(c.Mode,c.Preferred,pair[0],pair[1],"share","rub"); err!=nil { return err }
   instrument,err:=api.Instrument(ctx,pair[0],pair[1]); if err!=nil { return err }
   if !strings.EqualFold(instrument.Ticker,pair[0]) || !strings.EqualFold(instrument.ClassCode,pair[1]) { return fmt.Errorf("broker returned a different instrument") }
   if err:=policy.Check(c.Mode,c.Preferred,instrument.Ticker,instrument.ClassCode,instrument.Kind,instrument.Currency); err!=nil { return err }
   if !seen[instrument.UID] { metadata=append(metadata,instrument); ids=append(ids,instrument.UID); seen[instrument.UID]=true }
  }
  prices,err:=api.Prices(ctx,ids); if err!=nil { return err }
  byID:=map[string]tinvest.LastPrice{}; for _,price:=range prices.Prices { byID[price.UID]=price }
  for _,instrument:=range metadata {
   price,ok:=byID[instrument.UID]
   if !ok || price.Time.IsZero() { if _,err:=fmt.Fprintf(out,"%s:%s — quote unavailable\n",instrument.Ticker,instrument.ClassCode); err!=nil { return err }; continue }
   price.Price.Currency=instrument.Currency
   status:="last trade"; age:=time.Since(price.Time)
   if age>c.QuoteMaxAge { status="STALE" }; if age < -time.Minute { status="INVALID FUTURE TIMESTAMP" }
   lot,err:=lotPrice(price.Price,instrument.Lot); if err!=nil { return err }
   if _,err=fmt.Fprintf(out,"%s:%s | %s | unit=%s | lot=%d | lot price=%s | %s | %s\n",instrument.Ticker,instrument.ClassCode,instrument.Name,price.Price.String(),instrument.Lot,lot,price.Time.UTC().Format(time.RFC3339),status); err!=nil { return err }
  }
  return nil
 default: return fmt.Errorf("unknown command %q; run help",args[0])
 }
}
func flags(name string) *flag.FlagSet { fs:=flag.NewFlagSet(name,flag.ContinueOnError); fs.SetOutput(io.Discard); return fs }
func parse(fs *flag.FlagSet,args []string) error { if err:=fs.Parse(args); err!=nil { return err }; if fs.NArg()!=0 { return fmt.Errorf("unexpected positional arguments") }; return nil }
func checkAccount(ctx context.Context,api *tinvest.Client,id string) error {
 if id=="" { return fmt.Errorf("set TINVEST_ACCOUNT_ID explicitly") }
 accounts,err:=api.Accounts(ctx); if err!=nil { return err }
 for _,account:=range accounts.Accounts { if account.ID==id && account.Status=="ACCOUNT_STATUS_OPEN" { return nil } }
 return fmt.Errorf("configured account is not an open Sandbox account; inspect accounts (no automatic replacement)")
}
func printJSON(out io.Writer,v any) error { enc:=json.NewEncoder(out); enc.SetIndent("","  "); return enc.Encode(v) }
func lotPrice(m tinvest.Money,lot int32) (string,error) {
 value:=m.Units; if value=="" { value="0" }; units,ok:=new(big.Int).SetString(value,10); if !ok || lot<=0 || m.Nano<0 || m.Nano>=1000000000 || units.Sign()<0 { return "",fmt.Errorf("invalid quote") }
 scale:=big.NewInt(1000000000)
 total:=new(big.Int).Mul(units,scale); total.Add(total,big.NewInt(int64(m.Nano))); total.Mul(total,big.NewInt(int64(lot)))
 whole,fraction:=new(big.Int),new(big.Int); whole.QuoRem(total,scale,fraction)
 return fmt.Sprintf("%s.%09d %s",whole.String(),fraction.Int64(),m.Currency),nil
}
