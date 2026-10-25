package app

import (
 "context"
 "strings"
 "sort"
 "strconv"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/analysis"
 "ai-trader/internal/config"
 "ai-trader/internal/tinvest"
)

func enrichMarket(ctx context.Context,c config.Config,s *analysis.Snapshot,broker analysisBroker){
 now:=time.Now().UTC();day:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,time.UTC)
 // Fetch the available calendars once; a session alias from Instrument is
 // not necessarily a valid TradingSchedules request filter.
 schedules,scheduleErr:=broker.Schedules(ctx,"",day,day.Add(48*time.Hour))
 limit:=c.CandleLimit;if limit<=0{limit=24}
 for i:=range s.Quotes{
  q:=&s.Quotes[i]
  m:=&analysis.MarketContext{ObservedAt:time.Now().UTC(),Candles:[]tinvest.Candle{},Schedules:[]tinvest.Schedule{},Issues:[]string{}}
  q.Market=m
  status,err:=broker.TradingStatus(ctx,q.Instrument.UID)
  if err!=nil{m.Issues=append(m.Issues,"trading status unavailable: "+err.Error())}else{m.Status=&status}
  book,err:=broker.OrderBook(ctx,q.Instrument.UID)
  if err!=nil{m.Issues=append(m.Issues,"order book unavailable: "+err.Error())}else{
   if book.Currency=="" && book.UID==q.Instrument.UID && q.Instrument.Kind=="share" && strings.EqualFold(q.Instrument.Currency,"rub") {
    book.Currency=q.Instrument.Currency;book.CurrencySource="instrument_metadata"
   }else if book.Currency!=""{book.CurrencySource="broker_order_book"}
   m.Book=&book
  }
  candles,err:=broker.Candles(ctx,q.Instrument.UID,now.Add(-7*24*time.Hour),now)
  if err!=nil{m.Issues=append(m.Issues,"hourly candles unavailable: "+err.Error())}else{
   seen:=map[int64]bool{}
   for _,candle:=range candles.Candles{
    if !candle.Complete{continue}
    if !validCandle(candle,now)||seen[candle.Time.Unix()]{m.Issues=append(m.Issues,"invalid or duplicate hourly candle");continue}
    seen[candle.Time.Unix()]=true;m.Candles=append(m.Candles,candle)
   }
   sort.Slice(m.Candles,func(i,j int)bool{return m.Candles[i].Time.Before(m.Candles[j].Time)})
   if len(m.Candles)>limit{m.Candles=m.Candles[len(m.Candles)-limit:]}
  }
  exchange:=tinvest.ScheduleExchange(q.Instrument)
  if exchange==""{m.Issues=append(m.Issues,"instrument exchange is unknown");continue}
  if scheduleErr!=nil{m.Issues=append(m.Issues,"exchange schedule unavailable: "+scheduleErr.Error());continue}
  for _,schedule:=range schedules.Exchanges{
   if schedule.Exchange==q.Instrument.Exchange{m.Schedules=append(m.Schedules,schedule)}
  }
  if len(m.Schedules)==0{
   for _,schedule:=range schedules.Exchanges{if schedule.Exchange==exchange{m.Schedules=append(m.Schedules,schedule)}}
  }
  if len(m.Schedules)==0{m.Issues=append(m.Issues,"exchange schedule unavailable: no matching calendar for "+exchange)}
 }
}
func validCandle(c tinvest.Candle,now time.Time)bool{
 if c.Time.IsZero()||c.Time.Add(time.Hour).After(now){return false}
 open,e1:=amount.Parse(c.Open);high,e2:=amount.Parse(c.High);low,e3:=amount.Parse(c.Low);close,e4:=amount.Parse(c.Close)
 volume,e5:=strconv.ParseInt(c.Volume,10,64)
 return e1==nil&&e2==nil&&e3==nil&&e4==nil&&e5==nil&&volume>=0&&low>0&&high>=low&&open>=low&&open<=high&&close>=low&&close<=high
}
