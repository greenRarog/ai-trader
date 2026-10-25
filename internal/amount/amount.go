// Package amount uses signed nanorubles and checked integer arithmetic.
package amount

import (
 "fmt"
 "math/big"
 "strconv"

 "ai-trader/internal/tinvest"
)

const Scale int64 = 1000000000
func Parse(m tinvest.Money) (int64,error) {
 s:=m.Units; if s=="" { s="0" }
 u,ok:=new(big.Int).SetString(s,10)
 if !ok || m.Nano<=-1000000000 || m.Nano>=1000000000 { return 0,fmt.Errorf("invalid monetary value") }
 if (u.Sign()>0 && m.Nano<0)||(u.Sign()<0 && m.Nano>0) { return 0,fmt.Errorf("inconsistent monetary sign") }
 u.Mul(u,big.NewInt(Scale));u.Add(u,big.NewInt(int64(m.Nano)))
 if !u.IsInt64() { return 0,fmt.Errorf("monetary overflow") }; return u.Int64(),nil
}
func Add(a,b int64) (int64,error) { n:=new(big.Int).Add(big.NewInt(a),big.NewInt(b)); return checked(n) }
func Mul(a,b int64) (int64,error) { n:=new(big.Int).Mul(big.NewInt(a),big.NewInt(b)); return checked(n) }
func checked(n *big.Int) (int64,error) { if !n.IsInt64() { return 0,fmt.Errorf("monetary overflow") }; return n.Int64(),nil }
// BPS rounds a positive amount up when requested, conservatively reserving costs.
func BPS(value,bps int64,roundUp bool) (int64,error) {
 if value<0 || bps<0 { return 0,fmt.Errorf("negative cost input") }
 n:=new(big.Int).Mul(big.NewInt(value),big.NewInt(bps))
 if roundUp { n.Add(n,big.NewInt(9999)) }; n.Quo(n,big.NewInt(10000)); return checked(n)
}
func Text(n int64) string { return strconv.FormatInt(n,10) }
func Positive(s string) (int64,error) { n,e:=strconv.ParseInt(s,10,64);if e!=nil||n<=0 { return 0,fmt.Errorf("missing positive equity reference") };return n,nil }
func LossAtLeast(current,reference int64,percent int) bool {
 if reference<=0 || current>=reference { return false }
 loss:=new(big.Int).Sub(big.NewInt(reference),big.NewInt(current));loss.Mul(loss,big.NewInt(100))
 return loss.Cmp(new(big.Int).Mul(big.NewInt(reference),big.NewInt(int64(percent))))>=0
}
func ChangeBPS(current,reference int64) int64 {
 if reference<=0 { return 0 }
 n:=new(big.Int).Sub(big.NewInt(current),big.NewInt(reference));n.Mul(n,big.NewInt(10000));n.Quo(n,big.NewInt(reference))
 if !n.IsInt64() { return 0 };return n.Int64()
}


func CeilKopeck(value int64)(int64,error){
 if value<0{return 0,fmt.Errorf("negative commission")};const kopeck int64=10000000
 if value%kopeck==0{return value,nil};return Add(value,kopeck-value%kopeck)
}
