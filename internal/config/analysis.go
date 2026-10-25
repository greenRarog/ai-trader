package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Analysis struct {
	Provider string
	CodexBinary string
	CodexModel string
	DatabaseURL  string
	APIKey       string
	Model        string
	Timeout      time.Duration
	DailyLimit int
 CacheTTL time.Duration
 MaxInputBytes int
 Refresh bool // explicit CLI request, never enabled by the scheduler
 MaxTokens    int
}

func DatabaseURL() (string, error) {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" { return dsn, nil }
	password := os.Getenv("POSTGRES_PASSWORD")
	if password == "" { return "", fmt.Errorf("set DATABASE_URL or POSTGRES_PASSWORD") }
	host := value("POSTGRES_HOST", "postgres:5432")
 if os.Getenv("AI_TRADER_LOCAL") == "1" { host = "127.0.0.1:" + value("POSTGRES_PORT", "54329") }
 u := &url.URL{Scheme: "postgres", Host: host,
		Path: "/" + value("POSTGRES_DB", "ai_trader"),
		User: url.UserPassword(value("POSTGRES_USER", "ai_trader"), password)}
	q := url.Values{"sslmode": []string{"disable"}, "connect_timeout": []string{"10"}}
 // lib/pq accepts Unix socket directories through the host query parameter.
 // A filesystem path is not a valid URL authority.
 if strings.HasPrefix(host, "/") {
  u.Host = ""
  q.Set("host", host)
 }
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func LoadAnalysis() (Analysis, error) {
	c := Analysis{Provider: value("AI_PROVIDER", "openai_api"), APIKey: strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		Model: strings.TrimSpace(value("OPENAI_MODEL", "gpt-6-luna"))}
	var err error
 c.DailyLimit,err=strconv.Atoi(value("AI_DAILY_CALL_LIMIT","2"));if err!=nil||c.DailyLimit<1||c.DailyLimit>100{return c,fmt.Errorf("AI_DAILY_CALL_LIMIT must be 1..100")}
 c.CacheTTL,err=time.ParseDuration(value("AI_CACHE_TTL","10m"));if err!=nil||c.CacheTTL<=0||c.CacheTTL>10*time.Minute{return c,fmt.Errorf("AI_CACHE_TTL must be within (0,10m]")}
 c.MaxInputBytes,err=strconv.Atoi(value("AI_MAX_INPUT_BYTES","32768"));if err!=nil||c.MaxInputBytes<1024||c.MaxInputBytes>262144{return c,fmt.Errorf("AI_MAX_INPUT_BYTES must be 1024..262144")}
 if c.Provider == "codex_cli" {
  c.CodexBinary = strings.TrimSpace(value("CODEX_BINARY", "codex"))
  c.CodexModel = strings.TrimSpace(os.Getenv("CODEX_MODEL"))
  if c.CodexBinary == "" { return c, fmt.Errorf("CODEX_BINARY must not be empty") }
  c.Model = c.CodexModel
  if c.Model == "" { c.Model = "codex-default" }
  c.Timeout, err = time.ParseDuration(value("CODEX_TIMEOUT", "180s"))
  if err != nil || c.Timeout <= 0 || c.Timeout > 10*time.Minute { return c, fmt.Errorf("CODEX_TIMEOUT must be within (0, 10m]") }
  c.DatabaseURL, err = DatabaseURL()
  return c, err
 }
 if c.Provider != "openai_api" { return c, fmt.Errorf("AI_PROVIDER must be openai_api or codex_cli") }
	if c.APIKey == "" { return c, fmt.Errorf("set OPENAI_API_KEY in .env to run analyze") }
	if c.Model == "" { return c, fmt.Errorf("OPENAI_MODEL must not be empty") }
	c.Timeout, err = time.ParseDuration(value("OPENAI_TIMEOUT", "90s"))
	if err != nil || c.Timeout <= 0 || c.Timeout > 10*time.Minute { return c, fmt.Errorf("OPENAI_TIMEOUT must be within (0, 10m]") }
	c.MaxTokens, err = strconv.Atoi(value("OPENAI_MAX_OUTPUT_TOKENS", "2048"))
	if err != nil || c.MaxTokens < 128 || c.MaxTokens > 16384 { return c, fmt.Errorf("OPENAI_MAX_OUTPUT_TOKENS must be within 128..16384") }
	c.DatabaseURL, err = DatabaseURL()
	return c, err
}
