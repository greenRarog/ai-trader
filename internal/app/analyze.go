package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"ai-trader/internal/analysis"
	"ai-trader/internal/config"
 "ai-trader/internal/codexcli"
	"ai-trader/internal/openai"
	"ai-trader/internal/policy"
 "ai-trader/internal/risk"
	"ai-trader/internal/storage"
	"ai-trader/internal/tinvest"
)

// analysisBroker deliberately has no order or funding methods.
type analysisBroker interface {
	Accounts(context.Context) (tinvest.Accounts, error)
	Snapshot(context.Context, string, bool) (json.RawMessage, error)
	Instrument(context.Context, string, string) (tinvest.Instrument, error)
	Prices(context.Context, []string) (tinvest.Prices, error)
 Candles(context.Context,string,time.Time,time.Time)(tinvest.Candles,error)
 TradingStatus(context.Context,string)(tinvest.TradingStatus,error)
 OrderBook(context.Context,string)(tinvest.OrderBook,error)
 Schedules(context.Context,string,time.Time,time.Time)(tinvest.Schedules,error)
 OpenOrderCount(context.Context,string)(int,error)
}

type analysisStore interface {
	Begin(context.Context, string, string, string, string) (string, error)
	Prepare(context.Context, string, analysis.Snapshot, json.RawMessage) error
	Finish(context.Context, string, string, analysis.Result, string) error
 RecordEquity(context.Context,string,string,time.Time,int64)(analysis.EquityMetrics,error)
 VerifyAccount(context.Context,string,*analysis.AccountState,bool)error
 TradingControl(context.Context,string)(storage.TradingControl,error)
 ReserveModel(context.Context,string,string,bool,int,time.Duration)(analysis.Reservation,error)
}

type decisionModel interface {
	Respond(context.Context, json.RawMessage) (analysis.Result, error)
}

func runAnalyze(ctx context.Context, c config.Config, args []string, out io.Writer) error {
	fs := flags("analyze")
 refresh:=fs.Bool("refresh",false,"explicit extra model attempt: bypass duplicate cache and daily call budget")
	instruments := fs.String("instruments", strings.Join(c.Preferred, ","), "TICKER:CLASS candidates")
	if err := parse(fs, args); err != nil { return err }
	keys, err := candidateKeys(c, *instruments)
	if err != nil { return err }
	if c.AccountID == "" { return fmt.Errorf("set TINVEST_ACCOUNT_ID explicitly") }
	if c.Token == "" { return fmt.Errorf("TINVEST_TOKEN is required") }
	settings, err := config.LoadAnalysis()
	if err != nil { return err }
 settings.Refresh=*refresh
	db, err := storage.Open(ctx, settings.DatabaseURL)
	if err != nil { return err }
	defer db.Close()
 release,err:=db.AcquireAccount(ctx,c.AccountID);if err!=nil{return err};defer release()
	var model decisionModel = openai.New(settings.APIKey, settings.Timeout)
 if settings.Provider == "codex_cli" { model = codexcli.New(settings.CodexBinary, settings.CodexModel, settings.Timeout) }
 return analyze(ctx, c, settings, keys, tinvest.New(c.Token, c.Timeout), db, model, out)
}

func candidateKeys(c config.Config, input string) ([]string, error) {
	if strings.TrimSpace(input) == "" { return nil, fmt.Errorf("specify at least one candidate instrument") }
	keys := []string{}
	seen := map[string]bool{}
	for _, key := range strings.Split(input, ",") {
		key = strings.ToUpper(strings.TrimSpace(key))
		pair := strings.Split(key, ":")
		if len(pair) != 2 || pair[0] == "" || pair[1] == "" { return nil, fmt.Errorf("use TICKER:CLASS") }
		if err := policy.Check(c.Mode, c.Preferred, pair[0], pair[1], "share", "rub"); err != nil { return nil, err }
		if !seen[key] { keys = append(keys, key); seen[key] = true }
	}
	if len(keys) > 50 { return nil, fmt.Errorf("at most 50 candidate instruments per analysis") }
	return keys, nil
}

func analyze(ctx context.Context, c config.Config, settings config.Analysis, keys []string,
 broker analysisBroker, db analysisStore, model decisionModel, out io.Writer) error {
 provider:=settings.Provider;if provider==""{provider="openai_api"}
 var id string
 var err error
 if settings.Automatic {
  automatic,ok:=db.(interface{BeginAutomatic(context.Context,string,string,string,string)(string,error)})
  if !ok{return fmt.Errorf("automatic analysis requires durable execution provenance")}
  id,err=automatic.BeginAutomatic(ctx,c.AccountID,provider,settings.Model,analysis.PromptVersion)
 }else{id,err=db.Begin(ctx,c.AccountID,provider,settings.Model,analysis.PromptVersion)}
 if err!=nil{return err}
 if _,err:=fmt.Fprintln(out,"Analysis ID:",id);err!=nil{return err}
 economy:=&analysis.Economy{}
 finish:=func(status string,result analysis.Result,cause error)error{
  result.Provider=provider
  if provider!="manual_check"{result.Economy=economy}
  message:="";if cause!=nil{message=cause.Error()}
  auditCtx,cancel:=context.WithTimeout(context.WithoutCancel(ctx),10*time.Second);defer cancel()
  if err:=db.Finish(auditCtx,id,status,result,message);err!=nil{return fmt.Errorf("analysis %s: history update failed; inspect history before rerunning",id)}
  if store,ok:=db.(*storage.Store);ok&&status!="skipped"&&status!="reused"{
   message:="Анализ "+id+": "+status
   if result.Decision!=nil{message+=" · "+result.Decision.Action;if result.Decision.Ticker!=""{message+=" "+result.Decision.Ticker+fmt.Sprintf(" · %d лот(ов)",result.Decision.Lots)}}
   queueNotice(ctx,store,c.AccountID,"analysis:"+id,message,out)
  }
  if cause!=nil{return fmt.Errorf("analysis %s saved as %s: %w",id,status,cause)}
  summary:=result;summary.Raw=nil
  return printJSON(out,struct{ID string `json:"id"`;Status string `json:"status"`;ExecutionEnabled bool `json:"execution_enabled"`;Result analysis.Result `json:"result"`}{id,status,false,summary})
 }
 snapshot,err:=collectSnapshot(ctx,c,keys,broker);if err!=nil{return finish("broker_error",analysis.Result{},err)}
 if ledgerErr:=db.VerifyAccount(ctx,c.AccountID,snapshot.Account,false);ledgerErr!=nil{snapshot.Account.Issues=append(snapshot.Account.Issues,"account ledger mismatch: "+ledgerErr.Error())}
 metrics,err:=db.RecordEquity(ctx,c.AccountID,id,snapshot.CapturedAt,snapshot.Account.EquityNano);if err!=nil{return finish("broker_error",analysis.Result{},err)};snapshot.Equity=&metrics
 full,_:=json.Marshal(snapshot);economy.SnapshotBytes=len(full)
 localHold:=func(reasons []string)analysis.Result{return analysis.Result{Decision:&analysis.Decision{Action:"HOLD",Reason:"Локальная проверка: вызов ИИ пропущен. Подробности — в economy.reasons."}}}
 skip:=func(reasons []string)error{
  economy.Reasons=reasons
  request,_:=json.Marshal(map[string]any{"not_sent":true,"reasons":reasons})
  if err:=db.Prepare(ctx,id,snapshot,request);err!=nil{return fmt.Errorf("analysis %s: failed to persist skip context",id)}
  return finish("skipped",localHold(reasons),nil)
 }
 input:=snapshot
 if provider!="manual_check"{
  control,err:=db.TradingControl(ctx,c.AccountID);if err!=nil{return finish("broker_error",analysis.Result{},err)}
  if control.Paused{return skip([]string{"trading paused: "+control.Reason})}
  var reasons []string;input,reasons=eligibleContext(snapshot,c,time.Now().UTC())
  if len(input.Quotes)==0{if len(reasons)==0{reasons=[]string{"no eligible instruments"}};return skip(reasons)}
  // Keep excluded candidates in the full audit snapshot, not in the model prompt.
  economy.Reasons=reasons
  compact,err:=analysis.Compact(input);if err!=nil{return finish("broker_error",analysis.Result{},err)}
  compactJSON,err:=json.Marshal(compact);if err!=nil{return finish("model_error",analysis.Result{},err)};economy.InputBytes=len(compactJSON)
  maxBytes:=settings.MaxInputBytes;if maxBytes==0{maxBytes=32768}
  if len(compactJSON)>maxBytes{return skip([]string{"compact market context exceeds AI_MAX_INPUT_BYTES; reduce candidate instruments or CANDLE_LIMIT"})}
 }
 var request json.RawMessage
 if provider=="manual_check"{
  manual,ok:=model.(manualModel);if !ok{return finish("model_error",analysis.Result{},fmt.Errorf("invalid manual-check provider"))}
  request,err=json.Marshal(map[string]any{"provider":provider,"input":snapshot,"proposal":manual.decision,"mode":"risk preview only"})
 }else if provider=="codex_cli"{request,err=codexcli.BuildRequest(settings.CodexModel,input)}else{request,err=openai.BuildRequest(settings.Model,settings.MaxTokens,input)}
 if err!=nil{return finish("model_error",analysis.Result{},err)};economy.RequestBytes=len(request)
 if err:=db.Prepare(ctx,id,snapshot,request);err!=nil{return fmt.Errorf("analysis %s: failed to persist context; model was not called",id)}
 options:=risk.Options{QuoteMaxAge:c.QuoteMaxAge,MarketMaxAge:c.MarketMaxAge,SlippageBPS:c.SlippageBPS,LimitOrders:true}
 if provider!="manual_check"{
  compact,err:=analysis.Compact(input);if err!=nil{return finish("model_error",analysis.Result{},err)};fingerprint,err:=analysis.Fingerprint(compact);if err!=nil{return finish("model_error",analysis.Result{},err)}
  limit:=settings.DailyLimit;if limit==0{limit=2};ttl:=settings.CacheTTL;if ttl==0{ttl=10*time.Minute}
  reservation,err:=db.ReserveModel(ctx,id,fingerprint,settings.Refresh,limit,ttl);if err!=nil{return finish("model_error",analysis.Result{},err)}
  if !reservation.Attempt{
   economy.ReusedFrom=reservation.SourceID;economy.Reasons=append(economy.Reasons,reservation.Reason)
   if reservation.SourceStatus=="validated_proposal"&&reservation.SourceResult.Decision!=nil{
    result:=reservation.SourceResult;result.Raw=nil;result.Usage=nil
    report:=risk.Check(*result.Decision,input,time.Now().UTC(),options);result.Risk=&report
    if report.Allowed{return finish("reused",result,nil)}
    economy.Reasons=append(economy.Reasons,report.Reasons...)
   }
   return finish("skipped",localHold(economy.Reasons),nil)
  }
  economy.ModelAttempted=true
 }
 result,err:=model.Respond(ctx,request);if err!=nil{return finish("model_error",result,err)}
 if result.Decision==nil{return finish("model_error",result,fmt.Errorf("model returned no decision"))}
 if provider!="manual_check"&&len([]rune(result.Decision.Reason))>600{return finish("model_error",result,fmt.Errorf("model reason exceeds 600 characters; no automatic retry"))}
 report:=risk.Check(*result.Decision,input,time.Now().UTC(),options);result.Risk=&report
 if !report.Allowed{return finish("rejected",result,fmt.Errorf("risk check: %s",strings.Join(report.Reasons,"; ")))}
 return finish("validated_proposal",result,nil)
}

func collectSnapshot(ctx context.Context, c config.Config, keys []string, broker analysisBroker) (analysis.Snapshot, error) {
	s := analysis.Snapshot{CapturedAt: time.Now().UTC(), Mode: c.Mode,
		Preferred: append([]string{}, c.Preferred...), Constraints: analysis.DefaultConstraints(),
		Quotes: []analysis.Quote{}, Limitations: []string{
			"This snapshot is not an order; only a separate explicit Sandbox execution command can submit.",
			"No news or fundamentals; candles and order book are a limited market sample.",
   "Daily loss starts at first saved Moscow-day observation; high-water is sampled, not continuous.",
   "Explicitly recorded cash flows are removed from performance; unrecorded account changes block execution.",
			"Broker equity includes settled trading costs; future fees/slippage are estimates; taxes and model costs are not modelled.",
			"Positions and portfolio are sequential reads, not an atomic snapshot.",
		}}
 s.Constraints.SlippageBPS=c.SlippageBPS
	accounts, err := broker.Accounts(ctx)
	if err != nil { return s, err }
	found := false
	for _, account := range accounts.Accounts {
		if account.ID == c.AccountID && account.Status == "ACCOUNT_STATUS_OPEN" { found = true }
	}
	if !found { return s, fmt.Errorf("configured account is not an open Sandbox account") }
	s.Positions, err = broker.Snapshot(ctx, c.AccountID, false)
	if err != nil { return s, err }
	s.Portfolio, err = broker.Snapshot(ctx, c.AccountID, true)
	if err != nil { return s, err }
	// Broker account identifiers are unnecessary for the model's decision.
	s.Positions, err = withoutAccountID(s.Positions)
	if err != nil { return s, err }
	s.Portfolio, err = withoutAccountID(s.Portfolio)
	if err != nil { return s, err }
	ids := []string{}
	for _, key := range keys {
		pair := strings.Split(key, ":")
		i, err := broker.Instrument(ctx, pair[0], pair[1])
		if err != nil { return s, err }
		if i.Ticker != pair[0] || i.ClassCode != pair[1] || i.UID == "" || i.Lot <= 0 { return s, fmt.Errorf("invalid instrument metadata") }
		if err := policy.Check(c.Mode, c.Preferred, i.Ticker, i.ClassCode, i.Kind, i.Currency); err != nil { return s, err }
		s.Quotes = append(s.Quotes, analysis.Quote{Instrument: i})
		ids = append(ids, i.UID)
	}
	prices, err := broker.Prices(ctx, ids)
	if err != nil { return s, err }
	byID := map[string]tinvest.LastPrice{}
	for _, price := range prices.Prices { byID[price.UID] = price }
	now := time.Now().UTC()
	for index := range s.Quotes {
		q := &s.Quotes[index]
		price, ok := byID[q.Instrument.UID]
		if !ok || price.Time.IsZero() { q.Issue = "missing quote"; continue }
		price.Price.Currency = q.Instrument.Currency
		q.Price, q.Time = &price.Price, &price.Time
		units := price.Price.Units
		if units == "" { units = "0" }
		u, valid := new(big.Int).SetString(units, 10)
		switch {
		case !valid || u.Sign() < 0 || price.Price.Nano < 0 || price.Price.Nano >= 1000000000:
			q.Issue = "invalid price"
		case u.Sign() == 0 && price.Price.Nano == 0:
			q.Issue = "zero price"
		case now.Sub(price.Time) > c.QuoteMaxAge:
			q.Issue = "stale quote"
		case price.Time.After(now.Add(time.Minute)):
			q.Issue = "future timestamp"
		default:
			q.Usable = true
		}
	}
 orders,err:=broker.OpenOrderCount(ctx,c.AccountID)
 if err!=nil{return s,fmt.Errorf("read open Sandbox orders: %w",err)}
 s.Account,err=analysis.DecodeAccount(s.Positions,s.Portfolio,orders)
 if err!=nil{return s,err}
 enrichMarket(ctx,c,&s,broker)
 return s,nil
}

func withoutAccountID(data json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil { return nil, fmt.Errorf("invalid broker snapshot JSON") }
	delete(object, "accountId")
	return json.Marshal(object)
}

func runDatabase(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 || (args[0] != "migrate" && args[0] != "ping") { return fmt.Errorf("use db migrate or db ping") }
	dsn, err := config.DatabaseURL(); if err != nil { return err }
	db, err := storage.Open(ctx, dsn); if err != nil { return err }; defer db.Close()
	if args[0] == "migrate" { if err := db.Migrate(ctx); err != nil { return err } }
	_, err = fmt.Fprintln(out, "PostgreSQL:", args[0], "OK")
	return err
}

func runHistory(ctx context.Context, args []string, out io.Writer) error {
	fs := flags("history")
	id := fs.String("id", "", "analysis UUID (full saved record)")
	limit := fs.Int("limit", 20, "number of recent runs, 1..100")
	if err := parse(fs, args); err != nil { return err }
	if *limit < 1 || *limit > 100 { return fmt.Errorf("history limit must be within 1..100") }
	dsn, err := config.DatabaseURL(); if err != nil { return err }
	db, err := storage.Open(ctx, dsn); if err != nil { return err }; defer db.Close()
	data, err := db.History(ctx, *id, *limit); if err != nil { return err }
	return printJSON(out, data)
}
