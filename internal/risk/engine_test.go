package risk

import (
 "testing"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/tinvest"
)
func fixture()(analysis.Decision,analysis.Snapshot,time.Time,Options){
 now:=time.Date(2026,10,26,10,0,0,0,time.UTC);price:=tinvest.Money{Units:"100",Currency:"rub"};quoteTime:=now.Add(-time.Minute)
 s:=analysis.Snapshot{CapturedAt:now,Mode:"protective",Preferred:[]string{"SBER:TQBR"},Constraints:analysis.DefaultConstraints(),Account:&analysis.AccountState{EquityNano:100000*amount.Scale,AvailableRUBNano:100000*amount.Scale,Holdings:[]analysis.Holding{}},Equity:&analysis.EquityMetrics{ObservedAt:now,CurrentNano:"100000000000000",DayStartNano:"100000000000000",HighWaterNano:"100000000000000"},Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Ticker:"SBER",ClassCode:"TQBR",Kind:"share",Currency:"rub",Lot:10,Exchange:"MOEX"},Usable:true,Price:&price,Time:&quoteTime,Market:&analysis.MarketContext{ObservedAt:now,Status:&tinvest.TradingStatus{UID:"uid",Status:"SECURITY_TRADING_STATUS_NORMAL_TRADING",APIAvailable:true,MarketAvailable:true},Book:&tinvest.OrderBook{UID:"uid",Currency:"rub",Time:now,Asks:[]tinvest.BookLevel{{Price:price,Quantity:"100"}},Bids:[]tinvest.BookLevel{{Price:tinvest.Money{Units:"99"},Quantity:"100"}}},Schedules:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Trading:true,Start:now.Add(-time.Hour),End:now.Add(time.Hour)}}}}}}}}
 return analysis.Decision{Action:"BUY",Ticker:"SBER",ClassCode:"TQBR",Lots:1,Reason:"Тест"},s,now,Options{QuoteMaxAge:30*time.Minute,MarketMaxAge:time.Minute,SlippageBPS:20}
}
func TestBuyEstimates(t *testing.T){
 d,s,now,options:=fixture();r:=Check(d,s,now,options)
 if !r.Allowed||r.ExecutionEnabled||r.EstimatedValueNano!="1002000000000"||r.EstimatedCommissionNano!="510000000"||r.EstimatedCashChangeNano!="-1002510000000"{t.Fatalf("unexpected report: %+v",r)}
 d.Lots=20;options.SlippageBPS=0
 if r:=Check(d,s,now,options);!r.Allowed{t.Fatalf("exact position limit should pass: %+v",r)}
}
func TestRejectedBuyProposals(t *testing.T){
 cases:=[]struct{name string;change func(*analysis.Decision,*analysis.Snapshot,*Options)}{
  {"position size",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){d.Lots=20}},
  {"cash including fees",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.AvailableRUBNano=1002*amount.Scale}},
  {"daily exact stop",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.EquityNano=97000*amount.Scale;s.Equity.CurrentNano=amount.Text(s.Account.EquityNano)}},
  {"cost crosses stop",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.EquityNano=97001*amount.Scale;s.Equity.CurrentNano=amount.Text(s.Account.EquityNano)}},
  {"drawdown exact stop",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.EquityNano=85000*amount.Scale;s.Equity.CurrentNano=amount.Text(s.Account.EquityNano);s.Equity.DayStartNano=s.Equity.CurrentNano}},
  {"position count",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.Holdings=make([]analysis.Holding,5)}},
  {"latched daily stop",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Equity.DailyHalt=true}},
  {"latched drawdown stop",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Equity.DrawdownHalt=true}},
  {"open orders",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.OpenOrders=1}},
  {"unsafe account",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Account.Issues=[]string{"short holdings"}}},
  {"missing equity",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Equity=nil}},
  {"closed status",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Status.APIAvailable=false}},
  {"missing status",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Status=nil}},
  {"outside session",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Schedules=nil}},
  {"clearing",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){day:=&s.Quotes[0].Market.Schedules[0].Days[0];day.ClearingStart=day.Start;day.ClearingEnd=day.End}},
  {"stale status",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.ObservedAt=s.CapturedAt.Add(-2*time.Minute)}},
  {"stale book",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Book.Time=s.CapturedAt.Add(-2*time.Minute)}},
  {"missing liquidity",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Book.Asks=nil}},
  {"insufficient depth",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){d.Lots=2;s.Quotes[0].Market.Book.Asks[0].Quantity="1"}},
  {"crossed book",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Book.Bids[0].Price.Units="101"}},
  {"incomplete market data",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Quotes[0].Market.Issues=[]string{"schedule fetch failed"}}},
  {"instrument outside list",func(d *analysis.Decision,s *analysis.Snapshot,o *Options){s.Preferred=[]string{"GAZP:TQBR"}}},
 }
 for _,tt:=range cases{t.Run(tt.name,func(t *testing.T){d,s,now,o:=fixture();tt.change(&d,&s,&o);r:=Check(d,s,now,o);if r.Allowed||len(r.Reasons)==0{t.Fatalf("expected rejection: %+v",r)}})}
}
func TestSellReducesExposureAfterStop(t *testing.T){
 d,s,now,o:=fixture();d.Action="SELL";d.Lots=5
 s.Account.Holdings=[]analysis.Holding{{UID:"uid",Pieces:50,FreePieces:50,ValueNano:5000*amount.Scale}}
 s.Account.EquityNano=85000*amount.Scale;s.Equity.CurrentNano=amount.Text(s.Account.EquityNano)
 r:=Check(d,s,now,o);if !r.Allowed||r.EstimatedCashChangeNano==""{t.Fatalf("reducing sale blocked: %+v",r)}
 d.Lots=6;if r:=Check(d,s,now,o);r.Allowed{t.Fatal("short sale allowed")}
 s.Account.Holdings[0].FreePieces=40;d.Lots=5;if r:=Check(d,s,now,o);r.Allowed{t.Fatal("blocked shares sold")}
}
func TestHoldDoesNotRequireTradableMarket(t *testing.T){
 _,s,now,o:=fixture();s.Account=nil;s.Equity=nil;s.Quotes=nil
 if r:=Check(analysis.Decision{Action:"HOLD",Reason:"Биржа закрыта"},s,now,o);!r.Allowed||r.ExecutionEnabled{t.Fatal(r)}
}
func TestAddToExistingPositionAtFivePositionLimit(t *testing.T){
 d,s,now,o:=fixture();s.Account.Holdings=make([]analysis.Holding,5);s.Account.Holdings[0]=analysis.Holding{UID:"uid",Pieces:10,FreePieces:10,ValueNano:1000*amount.Scale}
 if r:=Check(d,s,now,o);!r.Allowed{t.Fatalf("existing position should be addable: %+v",r)}
}

func TestDealerSessionStaysBlocked(t *testing.T){
 d,s,now,o:=fixture();s.Quotes[0].Market.Status.Status="SECURITY_TRADING_STATUS_DEALER_NORMAL_TRADING"
 if r:=Check(d,s,now,o);r.Allowed{t.Fatal("dealer trading was silently enabled")}
}
func TestCalendarUsesRealExchange(t *testing.T){
 d,s,now,o:=fixture();s.Quotes[0].Instrument.Exchange="moex_mrng_evng_e_wknd_dlr";s.Quotes[0].Instrument.RealExchange="REAL_EXCHANGE_MOEX"
 if r:=Check(d,s,now,o);!r.Allowed{t.Fatalf("real exchange calendar was rejected: %+v",r)}
}

func TestLimitOrderAvailabilityIsIndependentOfMarketOrders(t *testing.T){
 d,s,now,o:=fixture();o.LimitOrders=true;s.Quotes[0].Market.Status.LimitAvailable=true;s.Quotes[0].Market.Status.MarketAvailable=false
 if r:=Check(d,s,now,o);!r.Allowed{t.Fatalf("available limit order was blocked by market flag: %+v",r)}
 s.Quotes[0].Market.Status.LimitAvailable=false;s.Quotes[0].Market.Status.MarketAvailable=true
 if r:=Check(d,s,now,o);r.Allowed{t.Fatal("unavailable limit order passed")}
}
