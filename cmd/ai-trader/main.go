package main

import (
 "context"
 "fmt"
 "os"
 "os/signal"
 "syscall"

 "ai-trader/internal/app"
 "ai-trader/internal/config"
)
func main() {
 if err:=run(); err!=nil { fmt.Fprintln(os.Stderr,"Error:",err); os.Exit(1) }
}
func run() error {
 args:=os.Args[1:]
 if len(args)==0 || args[0]=="help" || args[0]=="--help" || args[0]=="-h" { fmt.Print(app.Help); return nil }
 if err:=config.LoadEnv(".env"); err!=nil { return err }
 ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM); defer cancel()
 // Emergency pause/status must work even if unrelated model/risk configuration
 // is invalid. They require only the account identifier and database settings.
 if len(args)==2 && args[0]=="trading" && (args[1]=="pause" || args[1]=="status") {
  return app.Run(ctx,config.Config{AccountID:os.Getenv("TINVEST_ACCOUNT_ID")},args,os.Stdout)
 }
 c,err:=config.Load(); if err!=nil { return err }
 return app.Run(ctx,c,args,os.Stdout)
}
