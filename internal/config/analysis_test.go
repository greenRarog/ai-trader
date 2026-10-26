package config

import (
	"net/url"
	"testing"
)

func TestAnalysisConfiguration(t *testing.T) {
 t.Setenv("AI_PROVIDER", "openai_api")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := LoadAnalysis(); err == nil { t.Fatal("accepted missing API key") }
	t.Setenv("OPENAI_API_KEY", "fake")
	t.Setenv("OPENAI_MODEL", "test-model")
	t.Setenv("OPENAI_TIMEOUT", "90s")
	t.Setenv("OPENAI_MAX_OUTPUT_TOKENS", "2048")
	t.Setenv("DATABASE_URL", "postgres://unused")
	if _, err := LoadAnalysis(); err != nil { t.Fatal(err) }
	t.Setenv("OPENAI_TIMEOUT", "0s")
	if _, err := LoadAnalysis(); err == nil { t.Fatal("accepted invalid timeout") }
	t.Setenv("OPENAI_TIMEOUT", "90s")
	t.Setenv("OPENAI_MAX_OUTPUT_TOKENS", "-1")
	if _, err := LoadAnalysis(); err == nil { t.Fatal("accepted negative token budget") }
}

func TestDatabasePasswordEscaping(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_USER", "ai_trader")
	t.Setenv("POSTGRES_HOST", "postgres:5432")
	t.Setenv("POSTGRES_DB", "ai_trader")
	t.Setenv("POSTGRES_PASSWORD", "password/@:?#&")
	dsn, err := DatabaseURL(); if err != nil { t.Fatal(err) }
	u, err := url.Parse(dsn); if err != nil { t.Fatal(err) }
	password, _ := u.User.Password()
	if password != "password/@:?#&" || u.Query().Get("connect_timeout") != "10" { t.Fatal("DSN not escaped correctly") }
}

func TestCodexNeedsNoAPIKey(t *testing.T) {
 t.Setenv("AI_PROVIDER", "codex_cli")
 t.Setenv("OPENAI_API_KEY", "")
 t.Setenv("OPENAI_TIMEOUT", "invalid-and-unused")
 t.Setenv("DATABASE_URL", "postgres://unused")
 t.Setenv("CODEX_BINARY", "codex")
 t.Setenv("CODEX_MODEL", "")
 t.Setenv("CODEX_TIMEOUT", "180s")
 c, err := LoadAnalysis()
 if err != nil { t.Fatal(err) }
 if c.Provider != "codex_cli" || c.Model != "codex-default" || c.CodexModel != "" { t.Fatalf("wrong provider config: %+v", c) }
 t.Setenv("CODEX_MODEL", "explicit-model")
 c, err = LoadAnalysis()
 if err != nil || c.Model != "explicit-model" { t.Fatal("model override ignored") }
 t.Setenv("CODEX_TIMEOUT", "0s")
 if _, err := LoadAnalysis(); err == nil { t.Fatal("accepted zero timeout") }
 t.Setenv("AI_PROVIDER", "unknown")
 if _, err := LoadAnalysis(); err == nil { t.Fatal("accepted unknown provider") }
}

func TestLocalDatabaseAddress(t *testing.T) {
 t.Setenv("DATABASE_URL", "")
 t.Setenv("POSTGRES_PASSWORD", "local")
 t.Setenv("POSTGRES_HOST", "postgres:5432")
 t.Setenv("POSTGRES_PORT", "54329")
 t.Setenv("AI_TRADER_LOCAL", "1")
 dsn, err := DatabaseURL(); if err != nil { t.Fatal(err) }
 u, err := url.Parse(dsn); if err != nil { t.Fatal(err) }
 if u.Host != "127.0.0.1:54329" { t.Fatal("local launcher used container hostname") }
 t.Setenv("DATABASE_URL", "postgres://external.example/db")
 dsn, err = DatabaseURL()
 if err != nil || dsn != "postgres://external.example/db" { t.Fatal("explicit DSN override ignored") }
}


func TestUnixSocketDatabaseURL(t *testing.T) {
 t.Setenv("DATABASE_URL", "")
 t.Setenv("POSTGRES_PASSWORD", "test/@password")
 t.Setenv("POSTGRES_USER", "ai_trader")
 t.Setenv("POSTGRES_DB", "ai_trader")
 t.Setenv("POSTGRES_HOST", "/home/green/git/ai trader/.runtime/postgres")
 t.Setenv("AI_TRADER_LOCAL", "0")
 dsn, err := DatabaseURL(); if err != nil { t.Fatal(err) }
 u, err := url.Parse(dsn); if err != nil { t.Fatal(err) }
 if u.Host != "" || u.Query().Get("host") != "/home/green/git/ai trader/.runtime/postgres" { t.Fatal("socket path is not encoded in the DSN") }
 password, _ := u.User.Password()
 if password != "test/@password" { t.Fatal("password was corrupted") }
}

func TestEconomySettings(t *testing.T){
 t.Setenv("AI_PROVIDER","codex_cli");t.Setenv("DATABASE_URL","postgres://unused");t.Setenv("AI_DAILY_CALL_LIMIT","2");t.Setenv("AI_CACHE_TTL","10m");t.Setenv("AI_MAX_INPUT_BYTES","32768")
 c,err:=LoadAnalysis();if err!=nil||c.DailyLimit!=2||c.MaxInputBytes!=32768{t.Fatalf("%+v %v",c,err)}
 for _,tt:=range []struct{key,value string}{{"AI_DAILY_CALL_LIMIT","0"},{"AI_CACHE_TTL","11m"},{"AI_MAX_INPUT_BYTES","0"}}{t.Run(tt.key,func(t *testing.T){t.Setenv(tt.key,tt.value);if _,err:=LoadAnalysis();err==nil{t.Fatal("accepted invalid economy setting")}})}
}

func TestAutomaticExecutionMustBeExplicit(t *testing.T){
 t.Setenv("AI_PROVIDER","codex_cli");t.Setenv("DATABASE_URL","postgres://unused")
 t.Setenv("AUTOMATION_EXECUTE_ORDERS","false")
 c,err:=LoadAnalysis();if err!=nil||c.AutoExecute||c.Automatic{t.Fatal("execution enabled implicitly",err)}
 t.Setenv("AUTOMATION_EXECUTE_ORDERS","true")
 c,err=LoadAnalysis();if err!=nil||!c.AutoExecute||c.Automatic{t.Fatal("wrong execution configuration",err)}
 t.Setenv("AUTOMATION_EXECUTE_ORDERS","invalid")
 if _,err:=LoadAnalysis();err==nil{t.Fatal("invalid execution flag accepted")}
}
