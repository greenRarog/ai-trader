package analysis

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "sort"
 "strconv"
 "strings"
 "time"

 "ai-trader/internal/amount"
 "ai-trader/internal/tinvest"
)

// ModelContext contains only decision data. Full broker responses stay in the
// audit snapshot; neither backend receives raw portfolio/positions or account IDs.
type ModelContext struct {
 CapturedAt time.Time `json:"captured_at"`
 MoneyUnit string `json:"money_unit"`
 Mode string `json:"instrument_mode"`
 Preferred []string `json:"preferred_instruments"`
 Constraints Constraints `json:"constraints"`
 Account *ModelAccount `json:"account"`
 Equity *ModelEquity `json:"equity_metrics"`
 Quotes []ModelQuote `json:"quotes"`
 Limitations []string `json:"limitations"`
}
type ModelAccount struct {
 Equity string `json:"equity"`
 Cash string `json:"available_rub"`
 Holdings []ModelHolding `json:"holdings"`
 OpenOrders int `json:"open_orders"`
 Issues []string `json:"issues,omitempty"`
}
type ModelHolding struct {
 Ticker string `json:"ticker"`
 Class string `json:"class_code,omitempty"`
 Pieces int64 `json:"pieces"`
 Free int64 `json:"free_pieces"`
 Value string `json:"value"`
}
type ModelEquity struct {
 Adjusted string `json:"adjusted_equity"`
 DayStart string `json:"day_start"`
 HighWater string `json:"high_water"`
 DayStartAt time.Time `json:"day_start_at"`
 DailyChange int64 `json:"daily_change_bps"`
 Drawdown int64 `json:"drawdown_bps"`
 MaxDrawdown int64 `json:"max_drawdown_bps"`
 DailyHalt bool `json:"daily_halt"`
 DrawdownHalt bool `json:"drawdown_halt"`
 FlowAdjusted bool `json:"cashflow_adjusted"`
 Samples int64 `json:"samples"`
}
type ModelQuote struct {
 Ticker string `json:"ticker"`
 Class string `json:"class_code"`
 Lot int32 `json:"lot"`
 Price string `json:"price"`
 Time *time.Time `json:"price_time"`
 Usable bool `json:"usable"`
 Issue string `json:"issue,omitempty"`
 Status string `json:"trading_status,omitempty"`
 API bool `json:"api_available"`
 Limit bool `json:"limit_order_available"`
 BookTime time.Time `json:"book_time"`
 // Price, quantity in broker lots. All five supplied levels are retained.
 Bids [][2]string `json:"bids_price_lots"`
 Asks [][2]string `json:"asks_price_lots"`
 // time, open, high, low, close, volume in broker lots. No inferred signals.
 Candles [][6]string `json:"hourly_time_o_h_l_c_volume"`
 Sessions []map[string]any `json:"today_sessions"`
 Issues []string `json:"issues,omitempty"`
}

func rub(n int64) string {
 negative:=n<0
 // Avoid overflowing MinInt64 when formatting a signed amount.
 value:=amount.Text(n);if negative{value=strings.TrimPrefix(value,"-")}
 for len(value)<10{value="0"+value}
 i:=len(value)-9;whole,fraction:=value[:i],strings.TrimRight(value[i:],"0")
 if fraction!=""{whole+="."+fraction};if negative{whole="-"+whole};return whole
}
func money(m tinvest.Money)(string,error){n,err:=amount.Parse(m);if err!=nil{return "",err};return rub(n),nil}
func Compact(s Snapshot)(ModelContext,error){
 c:=ModelContext{CapturedAt:s.CapturedAt,MoneyUnit:"RUB decimal strings; quantities are pieces except lots and book/candle volume",Mode:s.Mode,Preferred:append([]string{},s.Preferred...),Constraints:s.Constraints,Quotes:[]ModelQuote{},Limitations:s.Limitations}
 sort.Strings(c.Preferred)
 if s.Account!=nil{
  a:=s.Account;c.Account=&ModelAccount{Equity:rub(a.EquityNano),Cash:rub(a.AvailableRUBNano),Holdings:[]ModelHolding{},OpenOrders:a.OpenOrders,Issues:a.Issues}
  for _,h:=range a.Holdings{holding:=ModelHolding{Ticker:h.Ticker,Pieces:h.Pieces,Free:h.FreePieces,Value:rub(h.ValueNano)};for _,q:=range s.Quotes{if q.Instrument.UID==h.UID{holding.Ticker=q.Instrument.Ticker;holding.Class=q.Instrument.ClassCode;break}};c.Account.Holdings=append(c.Account.Holdings,holding)}
  sort.Slice(c.Account.Holdings,func(i,j int)bool{a,b:=c.Account.Holdings[i],c.Account.Holdings[j];if a.Ticker!=b.Ticker{return a.Ticker<b.Ticker};return a.Class<b.Class})
 }
 if e:=s.Equity;e!=nil{
  parse:=func(v string)(string,error){if v==""{return "",nil};return nanoString(v)}
  adjusted:=e.AdjustedCurrentNano;if adjusted==""{adjusted=e.CurrentNano}
  a,e1:=parse(adjusted);d,e2:=parse(e.DayStartNano);h,e3:=parse(e.HighWaterNano);if e1!=nil||e2!=nil||e3!=nil{return c,fmt.Errorf("invalid equity metrics")}
  c.Equity=&ModelEquity{Adjusted:a,DayStart:d,HighWater:h,DayStartAt:e.DayStartAt,DailyChange:e.DailyChangeBPS,Drawdown:e.DrawdownBPS,MaxDrawdown:e.MaxDrawdownBPS,DailyHalt:e.DailyHalt,DrawdownHalt:e.DrawdownHalt,FlowAdjusted:e.CashflowAdjusted,Samples:e.Samples}
 }
 for _,q:=range s.Quotes{
  v:=ModelQuote{Ticker:q.Instrument.Ticker,Class:q.Instrument.ClassCode,Lot:q.Instrument.Lot,Time:q.Time,Usable:q.Usable,Issue:q.Issue,Bids:[][2]string{},Asks:[][2]string{},Candles:[][6]string{},Sessions:[]map[string]any{}}
  var err error;if q.Price!=nil{v.Price,err=money(*q.Price);if err!=nil{return c,err}}
  if m:=q.Market;m!=nil{
   v.Issues=m.Issues
   if m.Status!=nil{v.Status=m.Status.Status;v.API=m.Status.APIAvailable;v.Limit=m.Status.LimitAvailable}
   if m.Book!=nil{
    v.BookTime=m.Book.Time
    levels:=func(in []tinvest.BookLevel)([][2]string,error){out:=[][2]string{};for _,l:=range in{p,err:=money(l.Price);if err!=nil{return nil,err};out=append(out,[2]string{p,l.Quantity})};return out,nil}
    v.Bids,err=levels(m.Book.Bids);if err!=nil{return c,err};v.Asks,err=levels(m.Book.Asks);if err!=nil{return c,err}
   }
   for _,k:=range m.Candles{if !k.Complete{continue};o,e1:=money(k.Open);h,e2:=money(k.High);l,e3:=money(k.Low);close,e4:=money(k.Close);if e1!=nil||e2!=nil||e3!=nil||e4!=nil{return c,fmt.Errorf("invalid candle")};v.Candles=append(v.Candles,[6]string{k.Time.UTC().Format(time.RFC3339),o,h,l,close,k.Volume})}
   for _,schedule:=range m.Schedules{for _,d:=range schedule.Days{if d.Date.UTC().Format("2006-01-02")==s.CapturedAt.UTC().Format("2006-01-02")||d.OpenAt(s.CapturedAt){session:=map[string]any{"date":d.Date.UTC().Format("2006-01-02"),"trading":d.Trading}
     for name,t:=range map[string]time.Time{"start":d.Start,"end":d.End,"evening_start":d.EveningStart,"evening_end":d.EveningEnd,"clearing_start":d.ClearingStart,"clearing_end":d.ClearingEnd,"opening_start":d.OpeningStart,"opening_end":d.OpeningEnd,"closing_start":d.ClosingStart,"closing_end":d.ClosingEnd}{if !t.IsZero(){session[name]=t.UTC().Format(time.RFC3339)}}
     v.Sessions=append(v.Sessions,session)}}}
  }
  c.Quotes=append(c.Quotes,v)
 }
 sort.Slice(c.Quotes,func(i,j int)bool{a,b:=c.Quotes[i],c.Quotes[j];if a.Ticker!=b.Ticker{return a.Ticker<b.Ticker};return a.Class<b.Class})
 return c,nil
}

// Fingerprint ignores collection timestamps, not source quote/book/candle times,
// balances, constraints or loss latches. The sample counter alone is ignored. Eligibility is checked before reuse.
func Fingerprint(c ModelContext)(string,error){c.CapturedAt=time.Time{};if c.Equity!=nil{equity:=*c.Equity;equity.Samples=0;c.Equity=&equity};data,err:=json.Marshal(c);if err!=nil{return "",err};sum:=sha256.Sum256(data);return hex.EncodeToString(sum[:]),nil}

func nanoString(v string)(string,error){n,err:=strconv.ParseInt(v,10,64);if err!=nil{return "",err};return rub(n),nil}
