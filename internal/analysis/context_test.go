package analysis

import (
 "encoding/json"
 "strings"
 "testing"
 "time"
 "ai-trader/internal/tinvest"
)
func contextFixture()Snapshot{
 now:=time.Date(2026,10,26,12,0,0,0,time.UTC);p:=tinvest.Money{Units:"100",Nano:120000000,Currency:"rub"}
 m:=&MarketContext{ObservedAt:now,Book:&tinvest.OrderBook{UID:"private-uid",Time:now,Currency:"rub",Bids:[]tinvest.BookLevel{{Price:p,Quantity:"10"}},Asks:[]tinvest.BookLevel{{Price:p,Quantity:"20"}}}}
 for i:=0;i<24;i++{m.Candles=append(m.Candles,tinvest.Candle{Open:p,High:p,Low:p,Close:p,Time:now.Add(time.Duration(i-25)*time.Hour),Volume:"1234",Complete:true})}
 m.Schedules=[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Date:now.Truncate(24*time.Hour),Trading:true,Start:now.Add(-3*time.Hour),End:now.Add(3*time.Hour)}}}}
 return Snapshot{CapturedAt:now,Mode:"protective",Constraints:DefaultConstraints(),Account:&AccountState{EquityNano:100000000000000,AvailableRUBNano:90000000000000,Holdings:[]Holding{{UID:"private-uid",Ticker:"SBER",Pieces:100,FreePieces:90,ValueNano:10000000000000}}},Equity:&EquityMetrics{CurrentNano:"100000000000000",DayStartNano:"99000000000000",HighWaterNano:"101000000000000"},Positions:json.RawMessage(`{"accountId":"private-account","money":[{"units":"90000","nano":0,"currency":"rub"}]}`),Portfolio:json.RawMessage(`{"accountId":"private-account","totalAmountPortfolio":{"units":"100000","nano":0,"currency":"rub"}}`),Quotes:[]Quote{{Instrument:tinvest.Instrument{UID:"private-uid",Ticker:"SBER",ClassCode:"TQBR",Kind:"share",Currency:"rub",Lot:10},Price:&p,Time:&now,Usable:true,Market:m}}}
}
func TestCompactPreservesDecisionDataAndRemovesDuplicateBrokerJSON(t *testing.T){
 s:=contextFixture();c,err:=Compact(s);if err!=nil{t.Fatal(err)};data,_:=json.Marshal(c);full,_:=json.Marshal(s)
 if len(data)>=len(full)*8/10{t.Fatalf("compact=%d full=%d",len(data),len(full))}
 for _,private:=range []string{"private-account","private-uid","totalAmountPortfolio","instrumentType","quantityLots"}{if strings.Contains(string(data),private){t.Fatal("raw broker identifiers/fields leaked",private)}}
 if c.Account.Cash!="90000"||c.Account.Holdings[0].Free!=90||c.Quotes[0].Lot!=10||c.Quotes[0].Bids[0][0]!="100.12"||len(c.Quotes[0].Candles)!=24||c.Equity.DayStart!="99000"{t.Fatalf("lost decision data: %+v",c)}
 t.Logf("market input: full %d bytes, compact %d bytes (%.1f%% smaller)",len(full),len(data),100*(1-float64(len(data))/float64(len(full))))
}
func TestFingerprintIgnoresCollectionTimeButIncludesRiskAndSourceData(t *testing.T){
 s:=contextFixture();c,_:=Compact(s);first,_:=Fingerprint(c);c.CapturedAt=c.CapturedAt.Add(time.Minute);same,_:=Fingerprint(c);if first!=same{t.Fatal("collection time defeats deduplication")}
 c.Account.Cash="89999";different,_:=Fingerprint(c);if first==different{t.Fatal("cash change reused old decision")}
 c,_=Compact(s);c.Equity.DailyHalt=true;different,_=Fingerprint(c);if first==different{t.Fatal("loss halt reused old decision")}
 c,_=Compact(s);c.Quotes[0].BookTime=c.Quotes[0].BookTime.Add(time.Second);different,_=Fingerprint(c);if first==different{t.Fatal("new source data reused old decision")}
}
func TestRUBFormatting(t *testing.T){for _,tt:=range []struct{n int64;text string}{{0,"0"},{1,"0.000000001"},{1000000000,"1"},{-10000000,"-0.01"},{-9223372036854775808,"-9223372036.854775808"}}{if rub(tt.n)!=tt.text{t.Fatalf("%d: %s",tt.n,rub(tt.n))}}}
