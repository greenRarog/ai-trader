// Package report calculates read-only Sandbox analytics from the local journal.
package report

import (
 "fmt"
 "math/big"
 "sort"
 "time"
 "ai-trader/internal/amount"
)
type Equity struct{At time.Time;Value int64}
type Flow struct{At time.Time;Amount int64}
type Fill struct{ID,UID,Ticker,Action,State string;At time.Time;Pieces,Cash,Fee int64}
type Data struct{Equity []Equity;Flows []Flow;Fills []Fill;Issues []string}
type Position struct{UID string `json:"uid"`;Ticker string `json:"ticker"`;Pieces int64 `json:"pieces"`;Cost int64 `json:"cost_basis_nano"`}
type Closing struct{OrderID string `json:"order_id"`;Ticker string `json:"ticker"`;At time.Time `json:"order_created_at"`;Pieces int64 `json:"pieces"`;Proceeds int64 `json:"net_proceeds_nano"`;Cost int64 `json:"cost_basis_nano"`;PnL int64 `json:"realized_pnl_nano"`}
type Performance struct{FirstAt time.Time `json:"baseline_at"`;LastAt time.Time `json:"last_observation_at"`;First int64 `json:"baseline_equity_nano"`;Last int64 `json:"last_equity_nano"`;Flows int64 `json:"net_external_flows_nano"`;PnL int64 `json:"net_pnl_nano"`;ReturnBPS *int64 `json:"simple_return_bps"`;DrawdownBPS int64 `json:"sampled_max_drawdown_bps"`;Samples int `json:"samples"`}
type Summary struct{
 From time.Time `json:"from"`;To time.Time `json:"to"`;MoneyUnit string `json:"money_unit"`
 Performance *Performance `json:"performance"`
 JournalComplete bool `json:"journal_complete"`;Orders int `json:"confirmed_executed_orders"`;Fees int64 `json:"confirmed_commissions_nano"`;Turnover int64 `json:"confirmed_gross_turnover_nano"`
 Realized *int64 `json:"fifo_realized_pnl_nano"`
 Closings []Closing `json:"closing_orders"`;Positions []Position `json:"open_cost_basis"`
 Issues []string `json:"issues"`;Limitations []string `json:"limitations"`
}
type batch struct{pieces,cost int64}
func add(a *int64,b int64)error{v,err:=amount.Add(*a,b);if err==nil{*a=v};return err}
func allocate(cost,pieces,total int64)(int64,error){
 if cost<0||pieces<=0||total<pieces{return 0,fmt.Errorf("invalid FIFO allocation")}
 n:=new(big.Int).Mul(big.NewInt(cost),big.NewInt(pieces));n.Quo(n,big.NewInt(total));if !n.IsInt64(){return 0,fmt.Errorf("FIFO overflow")};return n.Int64(),nil
}
func Build(data Data,from,to time.Time)(Summary,error){
 s:=Summary{From:from,To:to,MoneyUnit:"signed integer nanorubles; 1 RUB = 1000000000; 100 basis points = 1%",Closings:[]Closing{},Positions:[]Position{},Issues:append([]string{},data.Issues...),Limitations:[]string{
 "Local journal only; no broker or model request. Unknown external operations are not imported.",
 "Equity profit removes recorded external flows; simple return uses opening capital, not TWR/IRR.",
 "Drawdown is sampled; first/last observation may differ from requested dates. No observation means unknown performance.",
 "FIFO uses cumulative confirmed fills ordered by order creation time; partial-fill timestamps are not reconstructed.",
 "Commissions are already included in trading cash effects and broker equity; do not deduct them twice.",
 "Open cost basis is not current market value; taxes, dividends, model currency costs and benchmarks are not calculated.",
 }}
 if from.IsZero()||!to.After(from){return s,fmt.Errorf("invalid report period")}
 // Copy before sorting to avoid mutating callers or sharing mutable slices.
 points:=append([]Equity{},data.Equity...);flows:=append([]Flow{},data.Flows...);fills:=append([]Fill{},data.Fills...)
 sort.SliceStable(points,func(i,j int)bool{return points[i].At.Before(points[j].At)})
 sort.SliceStable(flows,func(i,j int)bool{return flows[i].At.Before(flows[j].At)})
 sort.SliceStable(fills,func(i,j int)bool{if fills[i].At.Equal(fills[j].At){return fills[i].ID<fills[j].ID};return fills[i].At.Before(fills[j].At)})
 start:=-1;for i,p:=range points{if p.At.After(to){break};if start<0||!p.At.After(from){start=i}}
 if start>=0{
  selected:=[]Equity{};for _,p:=range points[start:]{if !p.At.After(to){selected=append(selected,p)}}
  if len(selected)>0{
   first:=selected[0];last:=selected[len(selected)-1];p:=&Performance{FirstAt:first.At,LastAt:last.At,First:first.Value,Last:last.Value,Samples:len(selected)}
   net:=int64(0);peak:=first.Value;flowIndex:=0
   for flowIndex<len(flows)&&!flows[flowIndex].At.After(first.At){flowIndex++}
   for _,point:=range selected{
    if point.Value<0{return s,fmt.Errorf("negative equity observation")}
    for flowIndex<len(flows)&&!flows[flowIndex].At.After(point.At){if err:=add(&net,flows[flowIndex].Amount);err!=nil{return s,err};flowIndex++}
    adjusted,err:=subtract(point.Value,net);if err!=nil{return s,err};if adjusted>peak{peak=adjusted}
    dd:=-amount.ChangeBPS(adjusted,peak);if dd>p.DrawdownBPS{p.DrawdownBPS=dd}
   }
   p.Flows=net;delta,err:=amount.Add(last.Value,-first.Value);if err!=nil{return s,err};p.PnL,err=subtract(delta,net);if err!=nil{return s,err}
   adjusted,err:=amount.Add(first.Value,p.PnL);if err!=nil{return s,err};if first.Value>0{bps:=amount.ChangeBPS(adjusted,first.Value);p.ReturnBPS=&bps}else{s.Issues=append(s.Issues,"zero opening capital: percentage return unavailable")};s.Performance=p
   if len(selected)<2{s.Issues=append(s.Issues,"only one equity observation; performance interval has no movement evidence")}
  }
 }
 inventory:=map[string][]batch{};tickers:=map[string]string{};realized:=int64(0);complete:=len(data.Issues)==0
 for _,fill:=range fills{
  if fill.At.After(to){continue};if fill.UID==""||fill.Pieces<=0||fill.Fee<0{return s,fmt.Errorf("invalid recorded fill")}
  if fill.State!="filled"&&fill.State!="cancelled"{complete=false;s.Issues=append(s.Issues,"unsettled executed order: "+fill.ID);continue}
  inPeriod:=!fill.At.Before(from);tickers[fill.UID]=fill.Ticker
  var gross int64;var err error
  switch fill.Action{
  case "BUY":
   if fill.Cash>=0||fill.Cash == -9223372036854775808{return s,fmt.Errorf("invalid purchase cash effect")}
   cost:=-fill.Cash;gross,err=amount.Add(cost,-fill.Fee);if err!=nil||gross<=0{return s,fmt.Errorf("invalid purchase total")}
   inventory[fill.UID]=append(inventory[fill.UID],batch{fill.Pieces,cost})
  case "SELL":
   if fill.Cash<0{return s,fmt.Errorf("negative sale proceeds")};gross,err=amount.Add(fill.Cash,fill.Fee);if err!=nil{return s,err}
   remaining:=fill.Pieces;cost:=int64(0);batches:=inventory[fill.UID]
   for remaining>0&&len(batches)>0{
    n:=remaining;if n>batches[0].pieces{n=batches[0].pieces};part,err:=allocate(batches[0].cost,n,batches[0].pieces);if err!=nil{return s,err}
    if err:=add(&cost,part);err!=nil{return s,err};batches[0].pieces-=n;batches[0].cost-=part;remaining-=n;if batches[0].pieces==0{batches=batches[1:]}
   };inventory[fill.UID]=batches
   if remaining!=0{complete=false;s.Issues=append(s.Issues,"sale without complete purchase basis: "+fill.ID)}else if inPeriod{
    pnl,err:=amount.Add(fill.Cash,-cost);if err!=nil{return s,err};if err:=add(&realized,pnl);err!=nil{return s,err};s.Closings=append(s.Closings,Closing{fill.ID,fill.Ticker,fill.At,fill.Pieces,fill.Cash,cost,pnl})
   }
  default:return s,fmt.Errorf("unknown recorded trade direction")
  }
  if inPeriod{s.Orders++;if err:=add(&s.Fees,fill.Fee);err!=nil{return s,err};if err:=add(&s.Turnover,gross);err!=nil{return s,err}}
 }
 for uid,batches:=range inventory{p:=Position{UID:uid,Ticker:tickers[uid]};for _,b:=range batches{if err:=add(&p.Pieces,b.pieces);err!=nil{return s,err};if err:=add(&p.Cost,b.cost);err!=nil{return s,err}};if p.Pieces>0{s.Positions=append(s.Positions,p)}}
 sort.Slice(s.Positions,func(i,j int)bool{return s.Positions[i].UID<s.Positions[j].UID})
 s.JournalComplete=complete
 if !complete{s.Performance=nil;s.Limitations=append(s.Limitations,"Performance withheld while account funding/execution reconciliation is unresolved.")}
 if s.Performance==nil&&len(data.Equity)==0{s.Issues=append(s.Issues,"no equity observations available in or before the requested period")}
 if complete{s.Realized=&realized}else{s.Closings=[]Closing{};s.Positions=[]Position{};s.Limitations=append(s.Limitations,"FIFO totals and open basis withheld because the journal is incomplete or has unresolved execution data.")}
 return s,nil
}

func subtract(a,b int64)(int64,error){n:=new(big.Int).Sub(big.NewInt(a),big.NewInt(b));if !n.IsInt64(){return 0,fmt.Errorf("analytics monetary overflow")};return n.Int64(),nil}
