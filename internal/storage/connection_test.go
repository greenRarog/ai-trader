package storage

import (
 "context"
 "fmt"
 "strings"
 "syscall"
 "testing"

 "github.com/lib/pq"
)

func TestConnectionDiagnosticsRedactCredentials(t *testing.T) {
 for _, tt := range []struct { cause error; expected string }{
  {fmt.Errorf("dial: %w", syscall.ECONNREFUSED), "connection refused"},
  {&pq.Error{Code: "28P01", Message: "secret-password"}, "authentication rejected"},
  {&pq.Error{Code: "3D000"}, "database does not exist"},
  {context.DeadlineExceeded, "connection timed out"},
 } {
  message := connectionError("postgres://user:secret-password@127.0.0.1:54329/db", tt.cause).Error()
  if !strings.Contains(message, tt.expected) || !strings.Contains(message, "127.0.0.1:54329") { t.Fatal(message) }
  if strings.Contains(message, "secret-password") { t.Fatal("credentials leaked") }
 }
}


func TestUnknownConnectionErrorIsUsefulAndRedacted(t *testing.T) {
 dsn := "postgres://user:p%40ss%2Fword@localhost/db?sslpassword=another-secret"
 cause := fmt.Errorf("protocol failure with p@ss/word, p%%40ss%%2Fword, another-secret, %s", dsn)
 message := connectionError(dsn, cause).Error()
 if !strings.Contains(message, "protocol failure") { t.Fatal("underlying cause is hidden") }
 for _, secret := range []string{"p@ss/word", "p%40ss%2Fword", "another-secret", dsn} {
  if strings.Contains(message, secret) { t.Fatal("credential leaked") }
 }
}
