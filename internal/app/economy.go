package app

import (
 "time"
 "ai-trader/internal/analysis"
 "ai-trader/internal/config"
 "ai-trader/internal/risk"
)

// eligibleContext filters only instruments for which at least one minimal order
// passes the existing Risk Engine. This is feasibility, never a trading signal.
func eligibleContext(s analysis.Snapshot,c config.Config,now time.Time)(analysis.Snapshot,[]string){
 filtered:=s;filtered.Quotes=[]analysis.Quote{};reasons:=[]string{}
 if s.Account==nil{return filtered,[]string{"account state unavailable"}}
 if len(s.Account.Issues)>0{return filtered,append(reasons,s.Account.Issues...)}
 if s.Account.OpenOrders!=0{return filtered,[]string{"unresolved/open orders; reconcile before analysis"}}
 options:=risk.Options{QuoteMaxAge:c.QuoteMaxAge,MarketMaxAge:c.MarketMaxAge,SlippageBPS:c.SlippageBPS,LimitOrders:true}
 for _,q:=range s.Quotes{
  key:=q.Instrument.Ticker+":"+q.Instrument.ClassCode
  if q.Market==nil||len(q.Market.Candles)==0{reasons=append(reasons,key+": completed hourly candles unavailable");continue}
  d:=analysis.Decision{Action:"BUY",Ticker:q.Instrument.Ticker,ClassCode:q.Instrument.ClassCode,Lots:1,Reason:"local feasibility check"}
  buy:=risk.Check(d,s,now,options)
  d.Action="SELL";sell:=risk.Check(d,s,now,options)
  if buy.Allowed||sell.Allowed{filtered.Quotes=append(filtered.Quotes,q)}else{
   reason:=key+": no admissible BUY or reducing SELL"
   if len(buy.Reasons)>0{reason+=" (BUY: "+buy.Reasons[0]+")"};if len(sell.Reasons)>0{reason+=" (SELL: "+sell.Reasons[0]+")"};reasons=append(reasons,reason)
  }
 }
 return filtered,reasons
}
