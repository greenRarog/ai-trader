// Package risk checks proposals; it has no order submission capability.
package risk

import (
 "strconv"
 "strings"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/tinvest"
)

type Options struct { LimitOrders bool; QuoteMaxAge,MarketMaxAge time.Duration; SlippageBPS int64 }
const SandboxCommissionBPS int64 = 5

func Check(d analysis.Decision,s analysis.Snapshot,now time.Time,options Options) analysis.RiskReport {
 report:=analysis.RiskReport{Reasons:[]string{},CommissionBPS:SandboxCommissionBPS,SlippageBPS:options.SlippageBPS}
 reject:=func(reason string)analysis.RiskReport{report.Reasons=append(report.Reasons,reason);return report}
 if err:=analysis.Validate(d,s,now,options.QuoteMaxAge);err!=nil{return reject(err.Error())}
 if d.Action=="HOLD"{report.Allowed=true;return report}
 if s.Constraints.MaxPositionRUB<=0||s.Constraints.MaxPositions<=0||s.Constraints.MaxDailyLossPct<=0||s.Constraints.MaxDrawdownPct<=0{return reject("invalid risk limits")}
 if options.MarketMaxAge<=0||options.SlippageBPS<0||options.SlippageBPS>1000{return reject("invalid risk configuration")}
 a:=s.Account
 if a==nil{return reject("account state is unavailable")}
 if len(a.Issues)>0{return reject("unsafe account state: "+strings.Join(a.Issues,"; "))}
 if a.OpenOrders!=0{return reject("open orders exist; potential exposure is uncertain")}
 if a.EquityNano<=0||a.AvailableRUBNano<0{return reject("nonpositive equity or negative cash")}
 var q *analysis.Quote
 for i:=range s.Quotes {if s.Quotes[i].Instrument.Ticker==d.Ticker&&s.Quotes[i].Instrument.ClassCode==d.ClassCode{q=&s.Quotes[i];break}}
 if q==nil||q.Instrument.UID==""||q.Instrument.Lot<=0{return reject("invalid instrument lot")}
 markPrice,markErr:=amount.Parse(*q.Price)
 if markErr!=nil||markPrice<=0||!strings.EqualFold(q.Price.Currency,"rub"){return reject("invalid RUB mark price")}
 market:=q.Market
 if market==nil||len(market.Issues)>0||!fresh(market.ObservedAt,now,options.MarketMaxAge){return reject("market context is missing, incomplete or stale")}
 status:=market.Status
 if status==nil||status.UID!=q.Instrument.UID||status.Status!="SECURITY_TRADING_STATUS_NORMAL_TRADING"||!status.APIAvailable||(options.LimitOrders&&!status.LimitAvailable)||(!options.LimitOrders&&!status.MarketAvailable){return reject("normal trading via API for the requested order type is unavailable")}
 scheduled:=false
 for _,exchange:=range market.Schedules {if exchange.Exchange!=q.Instrument.Exchange&&exchange.Exchange!=tinvest.ScheduleExchange(q.Instrument){continue};for _,day:=range exchange.Days{if day.OpenAt(now){scheduled=true}}}
 if !scheduled{return reject("outside verified trading schedule or during auction/clearing")}
 book:=market.Book
 if book==nil||book.UID!=q.Instrument.UID||!fresh(book.Time,now,options.MarketMaxAge)||!strings.EqualFold(book.Currency,"rub"){return reject("missing, stale or non-RUB order book")}
 if len(book.Asks)==0||len(book.Bids)==0{return reject("two-sided liquidity is unavailable")}
 ask,e1:=amount.Parse(book.Asks[0].Price);bid,e2:=amount.Parse(book.Bids[0].Price)
 if e1!=nil||e2!=nil||bid<=0||ask<bid{return reject("invalid or crossed order book")}
 pieces,err:=amount.Mul(d.Lots,int64(q.Instrument.Lot));if err!=nil||pieces<=0{return reject("share count overflow")};report.Pieces=pieces
 var existing,free int64
 for _,h:=range a.Holdings {if h.Pieces<0||h.FreePieces<0{return reject("short holdings are forbidden")};if h.UID==q.Instrument.UID{existing,free=h.Pieces,h.FreePieces}}
 if d.Action=="SELL"&&pieces>free{return reject("sale exceeds unblocked holdings; shorts are forbidden")}
 if d.Action=="BUY" {
  metrics:=s.Equity
  if metrics==nil||!fresh(metrics.ObservedAt,now,options.QuoteMaxAge)||metrics.CurrentNano!=amount.Text(a.EquityNano){return reject("equity history is missing, stale or inconsistent")}
  if metrics.DailyHalt{return reject("daily loss stop was triggered earlier today")}
  if metrics.DrawdownHalt{return reject("maximum sampled drawdown stop is latched for this account")}
  adjusted:=a.EquityNano
  if metrics.AdjustedCurrentNano!=""{var parseErr error;adjusted,parseErr=strconv.ParseInt(metrics.AdjustedCurrentNano,10,64);if parseErr!=nil{return reject("invalid adjusted equity")}}
  day,err:=amount.Positive(metrics.DayStartNano);if err!=nil{return reject("daily equity reference is unavailable")}
  high,err:=amount.Positive(metrics.HighWaterNano);if err!=nil{return reject("high-water mark is unavailable")}
  if amount.LossAtLeast(adjusted,day,s.Constraints.MaxDailyLossPct){return reject("daily loss limit reached; only HOLD or reducing SELL is permitted")}
  if amount.LossAtLeast(adjusted,high,s.Constraints.MaxDrawdownPct){return reject("drawdown limit reached; only HOLD or reducing SELL is permitted")}
  if existing==0&&len(a.Holdings)>=s.Constraints.MaxPositions{return reject("maximum number of open positions reached")}
  if len(a.Holdings)>s.Constraints.MaxPositions{return reject("existing position count exceeds limit")}
  limit,err:=amount.Mul(int64(s.Constraints.MaxPositionRUB),amount.Scale);if err!=nil||limit<=0{return reject("invalid position limit")}
  for _,h:=range a.Holdings {if h.ValueNano>limit{return reject("an existing position exceeds the position limit")}}
 }
 levels:=book.Asks;reference:=ask
 if d.Action=="SELL"{levels=book.Bids;reference=bid}
 buffer,err:=amount.BPS(reference,options.SlippageBPS,true);if err!=nil{return reject("price buffer overflow")}
 worst:=reference
 if d.Action=="BUY"{worst,err=amount.Add(reference,buffer)}else{worst=reference-buffer}
 if err!=nil||worst<=0{return reject("invalid conservative execution price")}
 if !hasLiquidity(levels,d.Lots,worst,d.Action=="BUY"){return reject("insufficient visible liquidity within slippage buffer")}
 notional,err:=amount.Mul(worst,pieces);if err!=nil{return reject("order value overflow")}
 commission,err:=amount.BPS(notional,SandboxCommissionBPS,true);if err!=nil{return reject("commission overflow")}
 commission,err=amount.CeilKopeck(commission);if err!=nil{return reject("commission rounding overflow")}
 report.EstimatedValueNano=amount.Text(notional);report.EstimatedCommissionNano=amount.Text(commission)
 if d.Action=="BUY"{
  projected,err:=amount.Add(existing,pieces);if err!=nil{return reject("position quantity overflow")}
  positionValue,err:=amount.Mul(projected,worst);if err!=nil{return reject("position value overflow")}
  limit,err:=amount.Mul(int64(s.Constraints.MaxPositionRUB),amount.Scale);if err!=nil||positionValue>limit{return reject("projected position exceeds maximum position size")}
  cash,err:=amount.Add(notional,commission);if err!=nil||cash>a.AvailableRUBNano{return reject("insufficient unblocked RUB including commission; leverage forbidden")}
  // Buying at an adverse price can immediately reduce marked equity.
  mark,err:=amount.Parse(*q.Price);if err!=nil||mark<=0{return reject("invalid mark price")}
  pricePenalty:=worst-mark;if pricePenalty<0{pricePenalty=0}
  penalty,err:=amount.Mul(pricePenalty,pieces);if err!=nil{return reject("valuation penalty overflow")}
  penalty,err=amount.Add(penalty,commission);if err!=nil{return reject("cost estimate overflow")}
  adjusted:=a.EquityNano
  if s.Equity.AdjustedCurrentNano!=""{adjusted,err=strconv.ParseInt(s.Equity.AdjustedCurrentNano,10,64);if err!=nil{return reject("invalid adjusted equity")}}
  projectedEquity,err:=amount.Add(adjusted,-penalty);if err!=nil||projectedEquity<=0{return reject("estimated costs exhaust equity")}
  day,_:=amount.Positive(s.Equity.DayStartNano);high,_:=amount.Positive(s.Equity.HighWaterNano)
  if amount.LossAtLeast(projectedEquity,day,s.Constraints.MaxDailyLossPct)||amount.LossAtLeast(projectedEquity,high,s.Constraints.MaxDrawdownPct){return reject("estimated spread, slippage and commission cross a loss limit")}
  report.EstimatedCashChangeNano=amount.Text(-cash)
 }else{
  if commission>notional{return reject("commission exceeds proceeds")}
  report.EstimatedCashChangeNano=amount.Text(notional-commission)
 }
 report.Allowed=true;return report
}
func fresh(t,now time.Time,maxAge time.Duration)bool{return !t.IsZero()&&maxAge>0&&now.Sub(t)<=maxAge&&!t.After(now.Add(time.Minute))}
func hasLiquidity(levels []tinvest.BookLevel,lots,bound int64,buy bool)bool{
 var available int64;var previous int64
 for i,level:=range levels{
  price,err:=amount.Parse(level.Price);if err!=nil||price<=0{return false}
  quantity,err:=strconv.ParseInt(level.Quantity,10,64);if err!=nil||quantity<=0{return false}
  if i>0&&((buy&&price<previous)||(!buy&&price>previous)){return false};previous=price
  if (buy&&price>bound)||(!buy&&price<bound){break}
  available,err=amount.Add(available,quantity);if err!=nil{return false};if available>=lots{return true}
 };return false
}
