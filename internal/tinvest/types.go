package tinvest

import (
 "fmt"
 "strconv"
 "strings"
 "time"
)

// Proto int64 values are encoded as JSON strings by the REST gateway.
type Money struct {
 Currency string `json:"currency,omitempty"`
 Units string `json:"units"`
 Nano int32 `json:"nano"`
}
func (m Money) String() string {
 units := m.Units; if units == "" { units = "0" }; u, err := strconv.ParseInt(units,10,64); if err != nil { return "invalid amount" }
 n := int64(m.Nano)
 sign := ""; if u<0 || n<0 { sign="-" }
 // Format without floating point or overflowing when negating MinInt64.
 digits := strings.TrimPrefix(units,"-")
 if n<0 { n=-n }
 return fmt.Sprintf("%s%s.%09d %s",sign,digits,n,m.Currency)
}
func ParseDeposit(s string) (Money,error) {
 parts := strings.Split(s,".")
 if len(parts)>2 || parts[0]=="" { return Money{},fmt.Errorf("invalid RUB amount") }
 for _, c := range parts[0] { if c<'0'||c>'9' { return Money{},fmt.Errorf("invalid RUB amount") } }
 units,err:=strconv.ParseInt(parts[0],10,64); if err!=nil || units>30000000 { return Money{},fmt.Errorf("deposit must be within (0, 30000000] RUB") }
 var nano int64
 if len(parts)==2 {
  if len(parts[1])==0 || len(parts[1])>2 { return Money{},fmt.Errorf("use at most two decimal places") }
  for _, c:=range parts[1] { if c<'0'||c>'9' { return Money{},fmt.Errorf("invalid RUB amount") } }
  nano,_=strconv.ParseInt(parts[1]+strings.Repeat("0",9-len(parts[1])),10,32)
 }
 if (units==0 && nano==0) || (units==30000000 && nano>0) { return Money{},fmt.Errorf("deposit must be within (0, 30000000] RUB") }
 return Money{Currency:"rub",Units:strconv.FormatInt(units,10),Nano:int32(nano)},nil
}
type Account struct { ID string `json:"id"`; Name string `json:"name"`; Status string `json:"status"` }
type Accounts struct { Accounts []Account `json:"accounts"` }
type Instrument struct {
 MinPriceIncrement Money `json:"minPriceIncrement"`
 RealExchange string `json:"realExchange"`
 Exchange string `json:"exchange"`
 UID string `json:"uid"`
 Ticker string `json:"ticker"`
 ClassCode string `json:"classCode"`
 Name string `json:"name"`
 Currency string `json:"currency"`
 Kind string `json:"instrumentType"`
 Lot int32 `json:"lot"`
}
type InstrumentResponse struct { Instrument Instrument `json:"instrument"` }
type LastPrice struct {
 UID string `json:"instrumentUid"`
 Price Money `json:"price"`
 Time time.Time `json:"time"`
}
type Prices struct { Prices []LastPrice `json:"lastPrices"` }
