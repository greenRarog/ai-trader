package config

import (
 "os"
 "path/filepath"
 "testing"
)
func TestInvalidConfig(t *testing.T) {
 t.Setenv("TINVEST_TOKEN","test")
 t.Setenv("REQUEST_TIMEOUT","15s")
 t.Setenv("QUOTE_MAX_AGE","30m")
 t.Setenv("INSTRUMENT_MODE","dangerous")
 if _,err:=Load(); err==nil { t.Fatal("accepted unknown mode") }
 t.Setenv("INSTRUMENT_MODE","protective"); t.Setenv("PREFERRED_INSTRUMENTS","")
 if _,err:=Load(); err==nil { t.Fatal("accepted empty protective list") }
 t.Setenv("INSTRUMENT_MODE","experimental")
 if _,err:=Load(); err!=nil { t.Fatal(err) }
 t.Setenv("REQUEST_TIMEOUT","0s")
 if _,err:=Load(); err==nil { t.Fatal("accepted zero timeout") }
}
func TestEnvPrecedence(t *testing.T) {
 t.Setenv("TINVEST_TOKEN","external-value")
 path:=filepath.Join(t.TempDir(),".env")
 if err:=os.WriteFile(path,[]byte("TINVEST_TOKEN=file-value\n"),0600); err!=nil { t.Fatal(err) }
 if err:=LoadEnv(path); err!=nil { t.Fatal(err) }
 if os.Getenv("TINVEST_TOKEN")!="external-value" { t.Fatal("overwrote environment") }
}
