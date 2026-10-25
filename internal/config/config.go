package config

import (
 "bufio"
 "fmt"
 "os"
 "strings"
 "strconv"
 "time"
)

type Config struct {
 CandleLimit int
 MarketMaxAge time.Duration
 SlippageBPS int64
 Token, AccountID, Mode string
 Preferred []string
 Timeout, QuoteMaxAge time.Duration
}

// LoadEnv reads simple KEY=VALUE entries without executing shell expressions.
// Environment variables supplied by the caller take precedence.
func LoadEnv(path string) error {
 f, err := os.Open(path)
 if os.IsNotExist(err) { return nil }; if err != nil { return err }; defer f.Close()
 s := bufio.NewScanner(f)
 for s.Scan() {
  line := strings.TrimSpace(s.Text())
  if line == "" || strings.HasPrefix(line, "#") { continue }
  k, v, ok := strings.Cut(line, "="); k = strings.TrimSpace(k)
  if !ok || k == "" { return fmt.Errorf("invalid .env entry") }
  v = strings.TrimSpace(v)
  if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) { v = v[1:len(v)-1] }
  if _, exists := os.LookupEnv(k); !exists { if err := os.Setenv(k,v); err != nil { return err } }
 }
 return s.Err()
}
func value(k, fallback string) string { if v, ok := os.LookupEnv(k); ok { return v }; return fallback }
func Load() (Config, error) {
 c := Config{Token:os.Getenv("TINVEST_TOKEN"), AccountID:os.Getenv("TINVEST_ACCOUNT_ID"), Mode:value("INSTRUMENT_MODE","protective")}
 if c.Mode != "protective" && c.Mode != "experimental" { return c, fmt.Errorf("INSTRUMENT_MODE must be protective or experimental") }
 for _, s := range strings.Split(value("PREFERRED_INSTRUMENTS","SBER:TQBR,GAZP:TQBR"),",") {
  s = strings.ToUpper(strings.TrimSpace(s)); if s == "" { continue }
  parts := strings.Split(s, ":"); if len(parts) != 2 || parts[0] == "" || parts[1] == "" { return c, fmt.Errorf("instrument must have TICKER:CLASS format") }
  c.Preferred = append(c.Preferred,s)
 }
 if c.Mode == "protective" && len(c.Preferred)==0 { return c,fmt.Errorf("protective mode requires a nonempty instrument list") }
 var err error
 c.Timeout, err = time.ParseDuration(value("REQUEST_TIMEOUT","15s")); if err != nil || c.Timeout<=0 { return c,fmt.Errorf("REQUEST_TIMEOUT must be a positive duration") }
 c.QuoteMaxAge, err = time.ParseDuration(value("QUOTE_MAX_AGE","30m")); if err != nil || c.QuoteMaxAge<=0 { return c,fmt.Errorf("QUOTE_MAX_AGE must be a positive duration") }
 c.CandleLimit,err=strconv.Atoi(value("CANDLE_LIMIT","24"));if err!=nil||c.CandleLimit<1||c.CandleLimit>168{return c,fmt.Errorf("CANDLE_LIMIT must be 1..168")}
 c.MarketMaxAge,err=time.ParseDuration(value("MARKET_MAX_AGE","60s"));if err!=nil||c.MarketMaxAge<=0||c.MarketMaxAge>5*time.Minute{return c,fmt.Errorf("MARKET_MAX_AGE must be within (0,5m]")}
 c.SlippageBPS,err=strconv.ParseInt(value("RISK_SLIPPAGE_BPS","20"),10,64);if err!=nil||c.SlippageBPS<0||c.SlippageBPS>1000{return c,fmt.Errorf("RISK_SLIPPAGE_BPS must be 0..1000")}
 return c,nil
}
