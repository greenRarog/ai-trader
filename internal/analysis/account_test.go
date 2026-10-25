package analysis

import (
 "encoding/json"
 "testing"
 "ai-trader/internal/amount"
)
func TestAccountUnblockedBalanceAndValuation(t *testing.T){
 positions:=json.RawMessage(`{"money":[{"currency":"rub","units":"99000"}],"securities":[{"instrumentUid":"uid","balance":"5","blocked":"5"}]}`)
 portfolio:=json.RawMessage(`{"totalAmountPortfolio":{"currency":"rub","units":"100000"},"positions":[{"instrumentType":"currency","ticker":"RUB000UTSTOM","quantity":{"units":"99000"},"currentPrice":{"units":"1","currency":"rub"}},{"instrumentUid":"uid","ticker":"SBER","instrumentType":"share","quantity":{"units":"10"},"currentPrice":{"units":"100","currency":"rub"}}]}`)
 a,err:=DecodeAccount(positions,portfolio,0);if err!=nil{t.Fatal(err)}
 if len(a.Issues)!=0||len(a.Holdings)!=1||a.Holdings[0].FreePieces!=5||a.Holdings[0].Pieces!=10||a.AvailableRUBNano!=99000*amount.Scale{t.Fatalf("wrong normalized account: %+v",a)}
}
func TestUnsafeAccountIsMarked(t *testing.T){
 positions:=json.RawMessage(`{"money":[{"currency":"rub","units":"-1"}],"futures":[{}],"limitsLoadingInProgress":true}`)
 portfolio:=json.RawMessage(`{"totalAmountPortfolio":{"currency":"rub","units":"100000"},"positions":[]}`)
 a,err:=DecodeAccount(positions,portfolio,1);if err!=nil{t.Fatal(err)}
 if len(a.Issues)<3||a.OpenOrders!=1{t.Fatalf("unsafe state was hidden: %+v",a)}
 if _,err:=DecodeAccount(json.RawMessage(`{}`),json.RawMessage(`{}`),0);err==nil{t.Fatal("missing valuation accepted")}
}
