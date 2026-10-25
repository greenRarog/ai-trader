package policy

import (
 "fmt"
 "strings"
)

// Check applies instrument selection only. It never authorizes an order.
func Check(mode string, preferred []string, ticker, class, kind, currency string) error {
 if kind != "share" || strings.ToLower(currency) != "rub" { return fmt.Errorf("only RUB shares are supported in stage 1") }
 switch mode {
 case "experimental": return nil
 case "protective":
  key := strings.ToUpper(ticker+":"+class)
  for _, allowed := range preferred { if key == allowed { return nil } }
  return fmt.Errorf("instrument %s is outside the protective list", key)
 default: return fmt.Errorf("unknown instrument mode")
 }
}
