package policy

import "testing"

func TestModes(t *testing.T) {
 tests:=[]struct { mode,ticker,class,kind,currency string; allowed bool }{
  {"protective","SBER","TQBR","share","rub",true},
  {"protective","OTHER","TQBR","share","rub",false},
  {"protective","SBER","OTHER","share","rub",false},
  {"experimental","OTHER","TQBR","share","rub",true},
  {"experimental","FUT","SPBFUT","futures","rub",false},
  {"experimental","OPT","SPBOPT","option","rub",false},
  {"experimental","SBER","TQBR","share","usd",false},
  {"invalid","SBER","TQBR","share","rub",false},
 }
 for _,tt:=range tests { t.Run(tt.mode+tt.ticker+tt.class+tt.kind+tt.currency,func(t *testing.T) {
  err:=Check(tt.mode,[]string{"SBER:TQBR"},tt.ticker,tt.class,tt.kind,tt.currency)
  if (err==nil)!=tt.allowed { t.Fatalf("allowed=%v, err=%v",tt.allowed,err) }
 }) }
}
