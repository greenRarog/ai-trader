package analysis

import (
 "testing"
 "time"
 "ai-trader/internal/amount"
)
func TestCashFlowsDoNotCreateProfit(t *testing.T){
 now:=time.Date(2026,10,26,9,0,0,0,time.UTC)
 m,err:=EquityFromPoints([]EquityPoint{{At:now,Raw:100000*amount.Scale,Adjusted:100000*amount.Scale},{At:now.Add(time.Minute),Raw:150000*amount.Scale,Adjusted:100000*amount.Scale},{At:now.Add(2*time.Minute),Raw:140000*amount.Scale,Adjusted:100000*amount.Scale}},100000*amount.Scale)
 if err!=nil||m.DailyChangeBPS!=0||m.MaxDrawdownBPS!=0||m.FlowAdjustmentNano!="40000000000000"{t.Fatalf("cash flow became a return: %+v %v",m,err)}
}
func TestAdjustedLossLatches(t *testing.T){
 now:=time.Date(2026,10,26,9,0,0,0,time.UTC)
 m,err:=EquityFromPoints([]EquityPoint{{At:now,Raw:100000*amount.Scale,Adjusted:100000*amount.Scale},{At:now.Add(time.Minute),Raw:135000*amount.Scale,Adjusted:85000*amount.Scale},{At:now.Add(2*time.Minute),Raw:149000*amount.Scale,Adjusted:99000*amount.Scale}},100000*amount.Scale)
 if err!=nil||!m.DailyHalt||!m.DrawdownHalt||m.MaxDrawdownBPS!=1500{t.Fatalf("funding hid a drawdown: %+v %v",m,err)}
}
