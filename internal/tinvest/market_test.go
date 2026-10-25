package tinvest

import (
 "context"
 "encoding/json"
 "io"
 "net/http"
 "testing"
 "time"
)
func TestMarketDataWireContracts(t *testing.T){
 now:=time.Date(2026,10,26,10,0,0,0,time.UTC)
 c:=fakeClient(t,func(w http.ResponseWriter,r *http.Request){
  var body map[string]json.RawMessage;if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil{t.Error(err)}
  switch r.URL.Path{
  case "/rest/"+marketService+"GetCandles":
   if _,exists:=body["limit"];exists{t.Error("limit is incompatible with exchange candle source") }
   if string(body["instrumentId"])!=`"uid"`||string(body["interval"])!=`"CANDLE_INTERVAL_HOUR"`||string(body["candleSourceType"])!=`"CANDLE_SOURCE_EXCHANGE"`{t.Error("wrong candle request")}
   io.WriteString(w,`{"candles":[{"open":{"units":"100"},"high":{"units":"101"},"low":{"units":"99"},"close":{"units":"100"},"volume":"10","time":"2026-10-26T08:00:00Z","isComplete":true}]}`)
  case "/rest/"+marketService+"GetTradingStatus":
   io.WriteString(w,`{"instrumentUid":"uid","tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING","apiTradeAvailableFlag":true,"marketOrderAvailableFlag":true}`)
  case "/rest/"+marketService+"GetOrderBook":
   if string(body["depth"])!="5"{t.Error("wrong depth")};io.WriteString(w,`{"instrumentUid":"uid","priceCurrency":"rub","orderbookTs":"2026-10-26T10:00:00Z","bids":[{"price":{"units":"99"},"quantity":"10"}],"asks":[{"price":{"units":"100"},"quantity":"10"}]}`)
  case "/rest/"+instrumentsService+"TradingSchedules":
   if string(body["exchange"])!=`"MOEX"`{t.Error("wrong exchange")};io.WriteString(w,`{"exchanges":[{"exchange":"MOEX","days":[{"isTradingDay":true,"startTime":"2026-10-26T07:00:00Z","endTime":"2026-10-26T15:00:00Z"}]}]}`)
  case "/rest/"+sandboxService+"GetSandboxOrders":io.WriteString(w,`{"orders":[]}`)
  default:t.Errorf("unexpected request: %s",r.URL.Path);w.WriteHeader(404)
  }
 })
 ctx:=context.Background()
 candles,err:=c.Candles(ctx,"uid",now.Add(-time.Hour),now);if err!=nil||len(candles.Candles)!=1||!candles.Candles[0].Complete{t.Fatalf("%+v %v",candles,err)}
 status,err:=c.TradingStatus(ctx,"uid");if err!=nil||!status.APIAvailable{t.Fatalf("%+v %v",status,err)}
 book,err:=c.OrderBook(ctx,"uid");if err!=nil||book.Time.IsZero()||len(book.Asks)!=1{t.Fatalf("%+v %v",book,err)}
 schedules,err:=c.Schedules(ctx,"MOEX",now,now.Add(24*time.Hour));if err!=nil||len(schedules.Exchanges)!=1||!schedules.Exchanges[0].Days[0].OpenAt(now){t.Fatalf("%+v %v",schedules,err)}
 orders,err:=c.OpenOrderCount(ctx,"account");if err!=nil||orders!=0{t.Fatalf("%d %v",orders,err)}
}
func TestScheduleBoundariesAndClearing(t *testing.T){
 now:=time.Now().UTC();d:=TradingDay{Trading:true,Start:now,End:now.Add(time.Hour)}
 if !d.OpenAt(now)||d.OpenAt(d.End){t.Fatal("session boundaries must be [start,end)")}
 d.ClearingStart=now;d.ClearingEnd=now.Add(time.Minute)
 if d.OpenAt(now){t.Fatal("clearing must close trading")}
 d.Trading=false;if d.OpenAt(now.Add(2*time.Minute)){t.Fatal("non-trading day was opened")}
}
