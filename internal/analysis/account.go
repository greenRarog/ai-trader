package analysis

import (
 "encoding/json"
 "fmt"
 "strconv"
 "strings"

 "ai-trader/internal/amount"
 "ai-trader/internal/tinvest"
)

func DecodeAccount(positions,portfolio json.RawMessage,openOrders int) (*AccountState,error) {
 var p struct {
  Money []tinvest.Money `json:"money"`
  Blocked []tinvest.Money `json:"blocked"`
  Securities []struct {
   UID string `json:"instrumentUid"`
   Balance string `json:"balance"`
   Blocked string `json:"blocked"`
   ExchangeBlocked bool `json:"exchangeBlocked"`
  } `json:"securities"`
  Futures []json.RawMessage `json:"futures"`
  Options []json.RawMessage `json:"options"`
  Loading bool `json:"limitsLoadingInProgress"`
 }
 var v struct {
  Total *tinvest.Money `json:"totalAmountPortfolio"`
  Positions []struct {
   UID string `json:"instrumentUid"`
   Ticker string `json:"ticker"`
   Kind string `json:"instrumentType"`
   Quantity tinvest.Money `json:"quantity"`
   Price tinvest.Money `json:"currentPrice"`
  } `json:"positions"`
 }
 if json.Unmarshal(positions,&p)!=nil || json.Unmarshal(portfolio,&v)!=nil || v.Total==nil || !strings.EqualFold(v.Total.Currency,"rub") { return nil,fmt.Errorf("missing or invalid RUB portfolio valuation") }
 equity,err:=amount.Parse(*v.Total);if err!=nil { return nil,err }
 state:=&AccountState{EquityNano:equity,OpenOrders:openOrders,Holdings:[]Holding{},Issues:[]string{}}
 issue:=func(s string){ state.Issues=append(state.Issues,s) }
 if p.Loading { issue("broker limits are loading") }
 if len(p.Futures)>0||len(p.Options)>0 { issue("derivative positions are not allowed") }
 if equity<0 { issue("negative equity") }
 for _,m:=range p.Money {
  n,e:=amount.Parse(m);if e!=nil { return nil,e }
  if n<0 { issue("negative cash balance / leverage") }
  if !strings.EqualFold(m.Currency,"rub") { if n!=0 { issue("non-RUB cash is unsupported") };continue }
  state.AvailableRUBNano,err=amount.Add(state.AvailableRUBNano,n);if err!=nil{return nil,err}
 }
 // Reserve blocked cash conservatively even if a broker contour already
 // excludes it from money. This can reject a proposal, never add buying power.
 for _,m:=range p.Blocked {
  n,e:=amount.Parse(m);if e!=nil||n<0 { return nil,fmt.Errorf("invalid blocked cash") }
  if strings.EqualFold(m.Currency,"rub") { state.AvailableRUBNano,err=amount.Add(state.AvailableRUBNano,-n);if err!=nil{return nil,err} }
 }
 if state.AvailableRUBNano<0 { issue("cash after reservations is negative") }
 free:=map[string]int64{};total:=map[string]int64{}
 for _,sec:=range p.Securities {
  balance,e:=parseCount(sec.Balance);if e!=nil{return nil,e};blocked,e:=parseCount(sec.Blocked);if e!=nil{return nil,e}
  if sec.UID=="" { issue("security is missing UID");continue }
  if _,ok:=free[sec.UID];ok { issue("duplicate security UID") }
  if balance<0||blocked<0 { issue("negative security balance / short position") }
  // PositionsSecurities.balance is explicitly the UNBLOCKED number of shares.
  free[sec.UID]=balance;if sec.ExchangeBlocked { free[sec.UID]=0 }
  total[sec.UID],err=amount.Add(balance,blocked);if err!=nil{return nil,err}
 }
 seen:=map[string]bool{};var valuation int64
 for _,pos:=range v.Positions {
  quantity,e:=amount.Parse(pos.Quantity);if e!=nil{return nil,e}
  if quantity==0 { continue }
  if quantity<0 { issue("negative portfolio quantity / short position") }
  price,e:=amount.Parse(pos.Price);if e!=nil{return nil,e}
  if price<=0||!strings.EqualFold(pos.Price.Currency,"rub") { issue("invalid or non-RUB position valuation") }
  // Multiply before division, with arbitrary precision handled by helper.
  value,e:=positionValue(quantity,price);if e!=nil{return nil,e}
  valuation,err=amount.Add(valuation,value);if err!=nil{return nil,err}
  if pos.Kind=="currency" { if !strings.EqualFold(pos.Ticker,"RUB000UTSTOM") { issue("non-RUB currency position") };continue }
  if pos.Kind!="share" { issue("unsupported non-share position");continue }
  if quantity%amount.Scale!=0 { issue("fractional shares are unsupported");continue }
  pieces:=quantity/amount.Scale
  if pos.UID==""||seen[pos.UID] { issue("missing or duplicate portfolio UID") };seen[pos.UID]=true
  if total[pos.UID]!=pieces { issue("positions and portfolio quantities disagree") }
  state.Holdings=append(state.Holdings,Holding{UID:pos.UID,Ticker:pos.Ticker,Pieces:pieces,FreePieces:free[pos.UID],ValueNano:value})
 }
 for uid,n:=range total { if n!=0&&!seen[uid] { issue("security is absent from portfolio valuation") } }
 difference,err:=amount.Add(valuation,-equity);if err!=nil{return nil,err}
 if difference>10000000||difference < -10000000 { issue("portfolio valuation is incomplete or inconsistent") }
 return state,nil
}
func parseCount(s string)(int64,error){if s==""{return 0,nil};n,e:=strconv.ParseInt(s,10,64);if e!=nil{return 0,fmt.Errorf("invalid share count")};return n,nil}
