package app

import (
 "context"
 "io"
 "testing"
 "strings"
 "time"

 "ai-trader/internal/config"
 "ai-trader/internal/tinvest"
)
func TestRejectedCommandsDoNotCallBroker(t *testing.T) {
 c:=config.Config{Token:"fake",Mode:"protective",Preferred:[]string{"SBER:TQBR"},Timeout:time.Millisecond}
 tests:=[]struct { args []string; message string }{
  {[]string{"quotes","--instruments","OTHER:TQBR"},"outside the protective list"},
  {[]string{"account","fund","--amount","100000"},"pass --confirm"},
  {[]string{"account","fund","--amount","100000","--confirm"},"set TINVEST_ACCOUNT_ID"},
  {[]string{"balance"},"set TINVEST_ACCOUNT_ID"},
  {[]string{"accounts","extra"},"accepts no arguments"},
 }
 for _,tt:=range tests {
  err:=Run(context.Background(),c,tt.args,io.Discard)
  if err==nil || !strings.Contains(err.Error(),tt.message) { t.Errorf("%v: expected %q, got %v",tt.args,tt.message,err) }
 }
}
func TestExactLotPrice(t *testing.T) {
 got,err:=lotPrice(tinvest.Money{Units:"123",Nano:456789123,Currency:"rub"},10)
 if err!=nil || got!="1234.567891230 rub" { t.Fatalf("%s %v",got,err) }
}
