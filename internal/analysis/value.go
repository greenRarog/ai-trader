package analysis

import (
 "fmt"
 "math/big"
 "ai-trader/internal/amount"
)
func positionValue(quantityNano,priceNano int64)(int64,error){
 n:=new(big.Int).Mul(big.NewInt(quantityNano),big.NewInt(priceNano));n.Quo(n,big.NewInt(amount.Scale))
 if !n.IsInt64(){return 0,fmt.Errorf("position valuation overflow")};return n.Int64(),nil
}
