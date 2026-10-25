package analysis

import (
 "fmt"
 "time"
 "ai-trader/internal/amount"
)
type EquityPoint struct{At time.Time;Raw,Adjusted int64}
// Metrics uses recorded, cash-flow-adjusted observations; it cannot reconstruct
// intraday movements before the first observation or between observations.
func EquityFromPoints(points []EquityPoint,initial int64)(EquityMetrics,error){
 var m EquityMetrics;if len(points)==0||initial<=0{return m,fmt.Errorf("equity history is empty")}
 last:=points[len(points)-1];moscow:=time.FixedZone("Europe/Moscow",3*3600);day:=last.At.In(moscow).Format("2006-01-02")
 peak:=initial;var baseline int64;dayFound:=false
 for _,p:=range points{
  if p.Adjusted>peak{peak=p.Adjusted}
  drawdown:=-amount.ChangeBPS(p.Adjusted,peak);if drawdown>m.MaxDrawdownBPS{m.MaxDrawdownBPS=drawdown}
  if p.At.In(moscow).Format("2006-01-02")==day{
   if !dayFound{baseline=p.Adjusted;m.DayStartAt=p.At;dayFound=true}
   if amount.LossAtLeast(p.Adjusted,baseline,DefaultConstraints().MaxDailyLossPct){m.DailyHalt=true}
  }
 }
 m.ObservedAt=last.At;m.CurrentNano=amount.Text(last.Raw);m.AdjustedCurrentNano=amount.Text(last.Adjusted);m.CashflowAdjusted=true
 adjustment,err:=amount.Add(last.Raw,-last.Adjusted);if err!=nil{return m,err};m.FlowAdjustmentNano=amount.Text(adjustment)
 m.DayStartNano=amount.Text(baseline);m.HighWaterNano=amount.Text(peak);m.DailyChangeBPS=amount.ChangeBPS(last.Adjusted,baseline);m.DrawdownBPS=-amount.ChangeBPS(last.Adjusted,peak);m.Samples=int64(len(points))
 m.DrawdownHalt=m.MaxDrawdownBPS>=int64(DefaultConstraints().MaxDrawdownPct)*100
 m.Basis="first saved Moscow-day observation; sampled high-water >= initial capital; explicitly recorded external cash flows removed"
 return m,nil
}
