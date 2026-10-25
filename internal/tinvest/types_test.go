package tinvest

import "testing"

func TestDeposit(t *testing.T) {
 for _,s:=range []string{"0","-1","NaN","1e5","1.001","30000000.01","999999999999999999999",".5","1."} {
  if _,err:=ParseDeposit(s); err==nil { t.Errorf("accepted %q",s) }
 }
 m,err:=ParseDeposit("100000.01"); if err!=nil { t.Fatal(err) }
 if m.Units!="100000" || m.Nano!=10000000 || m.Currency!="rub" { t.Fatalf("wrong amount: %+v",m) }
 if m.String()!="100000.010000000 rub" { t.Fatal(m.String()) }
}
