package report

import (
 "testing"
 "time"
 "ai-trader/internal/amount"
)
func TestFlowsAreNotProfitAndDrawdownIsSampled(t *testing.T){
 at:=time.Date(2026,10,26,10,0,0,0,time.UTC);rub:=amount.Scale
 d:=Data{Equity:[]Equity{{at,100000*rub},{at.Add(time.Hour),110000*rub},{at.Add(2*time.Hour),108000*rub}},Flows:[]Flow{{at.Add(30*time.Minute),10000*rub}}}
 s,err:=Build(d,at,at.Add(3*time.Hour));if err!=nil{t.Fatal(err)}
 if s.Performance==nil||s.Performance.PnL!=-2000*rub||s.Performance.ReturnBPS==nil||*s.Performance.ReturnBPS!=-200||s.Performance.DrawdownBPS!=200||s.Performance.Flows!=10000*rub{t.Fatalf("bad flow-adjusted report: %+v",s.Performance)}
 if d.Equity[0].Value!=100000*rub{t.Fatal("mutated report input")}
}
func TestFIFOIncludesBuyCommissionAndDoesNotDoubleCountSaleFee(t *testing.T){
 at:=time.Now().UTC().Add(-4*time.Hour);rub:=amount.Scale
 d:=Data{Fills:[]Fill{
  {ID:"buy1",UID:"sber",Ticker:"SBER",Action:"BUY",State:"filled",At:at,Pieces:100,Cash:-10005*rub,Fee:5*rub},
  {ID:"buy2",UID:"sber",Ticker:"SBER",Action:"BUY",State:"filled",At:at.Add(time.Hour),Pieces:100,Cash:-12006*rub,Fee:6*rub},
  {ID:"sell",UID:"sber",Ticker:"SBER",Action:"SELL",State:"cancelled",At:at.Add(2*time.Hour),Pieces:150,Cash:19490250000000,Fee:9750000000},
 }}
 s,err:=Build(d,at.Add(90*time.Minute),at.Add(3*time.Hour));if err!=nil{t.Fatal(err)}
 if !s.JournalComplete||s.Realized==nil||*s.Realized!=3482250000000||s.Fees!=9750000000||s.Turnover!=19500*rub||s.Orders!=1||len(s.Closings)!=1||len(s.Positions)!=1||s.Positions[0].Cost!=6003*rub||s.Positions[0].Pieces!=50{t.Fatalf("bad FIFO report: %+v",s)}
}
func TestUnknownBasisAndUnresolvedFlowsAreNeverReportedAsProfit(t *testing.T){
 at:=time.Now().UTC().Add(-time.Hour)
 s,err:=Build(Data{Fills:[]Fill{{ID:"unknown",UID:"x",Action:"SELL",State:"filled",At:at,Pieces:1,Cash:100}}},at,at.Add(time.Hour));if err!=nil{t.Fatal(err)}
 if s.Realized!=nil||s.JournalComplete||len(s.Positions)!=0{t.Fatal("invented cost basis")}
 s,err=Build(Data{Issues:[]string{"unresolved funding"},Equity:[]Equity{{at,100000},{at.Add(time.Minute),200000}}},at,at.Add(time.Hour));if err!=nil{t.Fatal(err)}
 if s.Performance!=nil||s.Realized!=nil{t.Fatal("unknown cash flow became profit")}
}
func TestFIFORoundingConservesExactCost(t *testing.T){
 at:=time.Now().UTC().Add(-time.Hour)
 d:=Data{Fills:[]Fill{{ID:"a",UID:"x",Action:"BUY",State:"filled",At:at,Pieces:3,Cash:-100},{ID:"b",UID:"x",Action:"SELL",State:"filled",At:at.Add(time.Minute),Pieces:1,Cash:40},{ID:"c",UID:"x",Action:"SELL",State:"filled",At:at.Add(2*time.Minute),Pieces:2,Cash:80}}}
 s,err:=Build(d,at,at.Add(time.Hour));if err!=nil||s.Realized==nil||*s.Realized!=20||len(s.Positions)!=0{t.Fatalf("rounding: %+v %v",s,err)}
}
func TestReportViewUsesExactRublesAndPercent(t *testing.T){
 profit:=int64(123450000000);bps:=int64(125);v,err:=View(Summary{Realized:&profit,Performance:&Performance{PnL:-10000000,ReturnBPS:&bps}});if err!=nil{t.Fatal(err)}
 fields:=v.(map[string]any);if fields["fifo_realized_pnl_rub"]!="123.45"{t.Fatal(fields)}
 performance:=fields["performance"].(map[string]any);if performance["net_pnl_rub"]!="-0.01"||performance["simple_return_pct"]!="1.25"{t.Fatal(performance)}
}
