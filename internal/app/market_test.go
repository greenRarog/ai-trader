package app

import (
 "context"
 "testing"
 "time"

 "ai-trader/internal/analysis"
 "ai-trader/internal/config"
 "ai-trader/internal/tinvest"
)
func TestCompletedCandlesOnly(t *testing.T){
 now:=time.Now().UTC();c:=tinvest.Candle{Open:tinvest.Money{Units:"100"},High:tinvest.Money{Units:"101"},Low:tinvest.Money{Units:"99"},Close:tinvest.Money{Units:"100"},Volume:"10",Time:now.Add(-2*time.Hour),Complete:true}
 if !validCandle(c,now){t.Fatal("valid candle rejected")}
 c.Time=now;if validCandle(c,now){t.Fatal("unfinished time interval admitted")}
 c.Time=now.Add(-2*time.Hour);c.High.Units="98";if validCandle(c,now){t.Fatal("invalid OHLC admitted")}
}
type failingMarketBroker struct{fakeBroker}
func(f failingMarketBroker)TradingStatus(context.Context,string)(tinvest.TradingStatus,error){return tinvest.TradingStatus{},context.DeadlineExceeded}
func TestMissingMarketDataIsPreservedForHold(t *testing.T){
 s:=analysis.Snapshot{Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Exchange:"MOEX"}}}}
 enrichMarket(context.Background(),config.Config{CandleLimit:24},&s,failingMarketBroker{})
 if s.Quotes[0].Market==nil||len(s.Quotes[0].Market.Issues)==0{t.Fatal("missing trading status was hidden")}
}

type actualMetadataBroker struct{fakeBroker}
func(f actualMetadataBroker)OrderBook(ctx context.Context,id string)(tinvest.OrderBook,error){book,err:=f.fakeBroker.OrderBook(ctx,id);book.Currency="";return book,err}
func(f actualMetadataBroker)Schedules(_ context.Context,exchange string,_ time.Time,_ time.Time)(tinvest.Schedules,error){
 if exchange!=""{return tinvest.Schedules{},context.Canceled}
 return tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Trading:true}}}}},nil
}
func TestBrokerSessionAliasAndMissingBookCurrency(t *testing.T){
 s:=analysis.Snapshot{Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Kind:"share",Currency:"rub",Exchange:"moex_mrng_evng_e_wknd_dlr",RealExchange:"REAL_EXCHANGE_MOEX"}}}}
 enrichMarket(context.Background(),config.Config{CandleLimit:24},&s,actualMetadataBroker{})
 m:=s.Quotes[0].Market
 if m==nil||len(m.Schedules)!=1||len(m.Issues)!=0{t.Fatalf("calendar mapping failed: %+v",m)}
 if m.Book==nil||m.Book.Currency!="rub"||m.Book.CurrencySource!="instrument_metadata"{t.Fatalf("currency provenance lost: %+v",m)}
}
func TestMarketFetchErrorIncludesDiagnostic(t *testing.T){
 s:=analysis.Snapshot{Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Exchange:"MOEX"}}}}
 enrichMarket(context.Background(),config.Config{},&s,failingMarketBroker{})
 if len(s.Quotes[0].Market.Issues)==0||s.Quotes[0].Market.Issues[0]!="trading status unavailable: context deadline exceeded"{t.Fatal("provider error was hidden")}
}


type scheduleRangeBroker struct{fakeBroker;t *testing.T}
func(f scheduleRangeBroker)Schedules(_ context.Context,_ string,from,to time.Time)(tinvest.Schedules,error){
 now:=time.Now().UTC();today:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,time.UTC)
 if from.Before(today)||!to.After(from){f.t.Error("schedule query starts before current date")}
 return tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX"}}},nil
}
func TestScheduleQueryStartsToday(t *testing.T){
 s:=analysis.Snapshot{Quotes:[]analysis.Quote{{Instrument:tinvest.Instrument{UID:"uid",Exchange:"MOEX"}}}}
 enrichMarket(context.Background(),config.Config{},&s,scheduleRangeBroker{t:t})
}
