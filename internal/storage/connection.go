package storage

import (
 "context"
 "errors"
 "fmt"
 "net"
 "net/url"
 "strings"
 "syscall"

 "github.com/lib/pq"
)

// Report actionable categories and redact credentials from diagnostic details.
func connectionError(dsn string, cause error) error {
 target := "configured server"
 if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
  host := u.Host
  if host == "" { host = u.Query().Get("host") }
  if host != "" && len(host) < 200 && !strings.ContainsAny(host, "\r\n") { target = host }
 }
 reason := "connection failed; inspect PostgreSQL logs and database configuration"
 var pgError *pq.Error
 var dnsError *net.DNSError
 var netError net.Error
 switch {
 case errors.As(cause, &pgError):
  switch string(pgError.Code) {
  case "28P01", "28000":
   reason = "authentication rejected; POSTGRES_PASSWORD in .env may differ from the password stored in the existing PostgreSQL volume"
  case "3D000":
   reason = "database does not exist; check POSTGRES_DB"
  case "57P03":
   reason = "server is starting; wait for PostgreSQL readiness"
  }
 case errors.Is(cause, syscall.ECONNREFUSED):
  reason = "connection refused; start/recreate postgres and check its published loopback port"
 case errors.As(cause, &dnsError):
  reason = "hostname cannot be resolved; use scripts/trader.sh for local access or the postgres hostname only inside Compose"
 case errors.Is(cause, context.DeadlineExceeded):
  reason = "connection timed out; check server readiness and published port"
 case errors.As(cause, &netError) && netError.Timeout():
  reason = "connection timed out; check server readiness and published port"
 case errors.Is(cause, context.Canceled):
  reason = "connection cancelled"
 }
 return fmt.Errorf("cannot connect to PostgreSQL at %s: %s; detail: %s", target, reason, safeConnectionDetail(dsn, cause))
}


func safeConnectionDetail(dsn string, cause error) string {
 if cause == nil { return "unknown error" }
 u, err := url.Parse(dsn)
 // Keyword DSNs cannot be redacted reliably without parsing the full grammar.
 if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") { return fmt.Sprintf("%T", cause) }
 detail := cause.Error()
 secrets := []string{dsn}
 if u.User != nil {
  if password, ok := u.User.Password(); ok && password != "" {
   secrets = append(secrets, password, url.QueryEscape(password), url.PathEscape(password))
  }
 }
 for key, values := range u.Query() { if strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "token") { for _, value := range values { if value != "" { secrets = append(secrets, value, url.QueryEscape(value)) } } } }
 for _, secret := range secrets { detail = strings.ReplaceAll(detail, secret, "[redacted]") }
 detail = strings.Map(func(r rune) rune { if r < 32 || r == 127 { return ' ' }; return r }, detail)
 if len(detail) > 512 { detail = detail[:512] + "..." }
 return fmt.Sprintf("%T: %s", cause, detail)
}
