package app

import (
 "context"
 "fmt"
 "io"
 "time"
 "ai-trader/internal/config"
 "ai-trader/internal/report"
)
func runReport(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 fs:=flags("report");days:=fs.Int("days",30,"Moscow calendar days including today, 1..3650")
 if err:=parse(fs,args);err!=nil{return err};if *days<1||*days>3650{return fmt.Errorf("report --days must be 1..3650")}
 now:=time.Now().UTC();moscow:=time.FixedZone("Europe/Moscow",3*3600);local:=now.In(moscow);from:=time.Date(local.Year(),local.Month(),local.Day(),0,0,0,0,moscow).AddDate(0,0,-(*days-1)).UTC()
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 data,err:=db.ReportData(ctx,c.AccountID,now);if err!=nil{return err};summary,err:=report.Build(data,from,now);if err!=nil{return err};view,err:=report.View(summary);if err!=nil{return err};return printJSON(out,view)
}
