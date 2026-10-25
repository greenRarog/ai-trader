package amount

import (
 "math"
 "testing"
 "ai-trader/internal/tinvest"
)
func TestExactAmountsAndOverflow(t *testing.T){
 n,err:=Parse(tinvest.Money{Units:"100000",Nano:1});if err!=nil||n!=100000000000001{t.Fatalf("%d %v",n,err)}
 if _,err:=Parse(tinvest.Money{Units:"1",Nano:-1});err==nil{t.Fatal("accepted inconsistent sign")}
 if _,err:=Mul(math.MaxInt64,2);err==nil{t.Fatal("missed multiplication overflow")}
 if _,err:=Add(math.MaxInt64,1);err==nil{t.Fatal("missed addition overflow")}
 fee,err:=BPS(1000000001,5,true);if err!=nil||fee!=500001{t.Fatalf("wrong upward fee rounding: %d %v",fee,err)}
 if !LossAtLeast(97000*Scale,100000*Scale,3)||LossAtLeast(97000*Scale+1,100000*Scale,3){t.Fatal("daily threshold is not exact")}
 if ChangeBPS(85000*Scale,100000*Scale)!=-1500{t.Fatal("wrong drawdown")}
}
