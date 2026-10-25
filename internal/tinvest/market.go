package tinvest

import (
 "context"
 "fmt"
 "time"
)

type Candle struct {
 Open Money `json:"open"`
 High Money `json:"high"`
 Low Money `json:"low"`
 Close Money `json:"close"`
 Volume string `json:"volume"`
 Time time.Time `json:"time"`
 Complete bool `json:"isComplete"`
}
type Candles struct { Candles []Candle `json:"candles"` }
type TradingStatus struct {
 UID string `json:"instrumentUid"`
 Status string `json:"tradingStatus"`
 APIAvailable bool `json:"apiTradeAvailableFlag"`
 MarketAvailable bool `json:"marketOrderAvailableFlag"`
 LimitAvailable bool `json:"limitOrderAvailableFlag"`
}
type BookLevel struct { Price Money `json:"price"`; Quantity string `json:"quantity"` }
type OrderBook struct {
 CurrencySource string `json:"currency_source,omitempty"`
 UID string `json:"instrumentUid"`
 Bids []BookLevel `json:"bids"`
 Asks []BookLevel `json:"asks"`
 Time time.Time `json:"orderbookTs"`
 Currency string `json:"priceCurrency"`
}
type TradingDay struct {
 Date time.Time `json:"date"`
 Trading bool `json:"isTradingDay"`
 Start time.Time `json:"startTime"`
 End time.Time `json:"endTime"`
 EveningStart time.Time `json:"eveningStartTime"`
 EveningEnd time.Time `json:"eveningEndTime"`
 ClearingStart time.Time `json:"clearingStartTime"`
 ClearingEnd time.Time `json:"clearingEndTime"`
 OpeningStart time.Time `json:"openingAuctionStartTime"`
 OpeningEnd time.Time `json:"openingAuctionEndTime"`
 ClosingStart time.Time `json:"closingAuctionStartTime"`
 ClosingEnd time.Time `json:"closingAuctionEndTime"`
}
type Schedule struct { Exchange string `json:"exchange"`; Days []TradingDay `json:"days"` }
type Schedules struct { Exchanges []Schedule `json:"exchanges"` }
func (d TradingDay) OpenAt(now time.Time) bool {
 inside:=func(start,end time.Time) bool { return !start.IsZero() && end.After(start) && !now.Before(start) && now.Before(end) }
 if !d.Trading || inside(d.ClearingStart,d.ClearingEnd) || inside(d.OpeningStart,d.OpeningEnd) || inside(d.ClosingStart,d.ClosingEnd) { return false }
 return inside(d.Start,d.End)||inside(d.EveningStart,d.EveningEnd)
}
func (c *Client) Candles(ctx context.Context,id string,from,to time.Time) (Candles,error) {
 var out Candles
 err:=c.call(ctx,marketService+"GetCandles",map[string]any{"instrumentId":id,"from":from.UTC().Format(time.RFC3339),"to":to.UTC().Format(time.RFC3339),"interval":"CANDLE_INTERVAL_HOUR","candleSourceType":"CANDLE_SOURCE_EXCHANGE"},&out,true)
 return out,err
}
func (c *Client) TradingStatus(ctx context.Context,id string) (TradingStatus,error) {
 var out TradingStatus;err:=c.call(ctx,marketService+"GetTradingStatus",map[string]string{"instrumentId":id},&out,true);return out,err
}
func (c *Client) OrderBook(ctx context.Context,id string) (OrderBook,error) {
 var out OrderBook;err:=c.call(ctx,marketService+"GetOrderBook",map[string]any{"instrumentId":id,"depth":5},&out,true);return out,err
}
func (c *Client) Schedules(ctx context.Context,exchange string,from,to time.Time) (Schedules,error) {
 var out Schedules;err:=c.call(ctx,instrumentsService+"TradingSchedules",map[string]string{"exchange":exchange,"from":from.UTC().Format(time.RFC3339),"to":to.UTC().Format(time.RFC3339)},&out,true);return out,err
}
func (c *Client) OpenOrderCount(ctx context.Context,id string) (int,error) {
 var out struct { Orders *[]struct{ ID string `json:"orderId"` } `json:"orders"` }
 err:=c.call(ctx,sandboxService+"GetSandboxOrders",map[string]string{"accountId":id},&out,true);if err!=nil{return 0,err};if out.Orders==nil{return 0,fmt.Errorf("missing Sandbox orders list")};return len(*out.Orders),nil
}


// Instrument.exchange can be a broker session alias; realExchange identifies
// the exchange for the general calendar. Unknown values are never guessed.
func ScheduleExchange(instrument Instrument) string {
 switch instrument.RealExchange {
 case "REAL_EXCHANGE_MOEX": return "MOEX"
 default: return instrument.Exchange
 }
}
