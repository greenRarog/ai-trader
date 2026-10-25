package app

import (
 "context"
 "errors"
 "fmt"
 "io"
 "time"
 "ai-trader/internal/config"
 "ai-trader/internal/execution"
 "ai-trader/internal/notify"
 "ai-trader/internal/storage"
)
// Notification failures are isolated from trading/accounting outcomes. Unknown
// send outcomes remain visible in the outbox and are never blindly resent.
func queueNotice(ctx context.Context,db *storage.Store,account,key,message string,out io.Writer){
 settings,err:=config.LoadTelegram();if err!=nil{fmt.Fprintln(out,"Warning: Telegram configuration invalid; notification not queued");return};if !settings.Enabled{return}
 audit,cancel:=context.WithTimeout(context.WithoutCancel(ctx),20*time.Second);defer cancel()
 if err:=db.EnqueueNotice(audit,account,key,settings.ChatID,"AI Trader · Sandbox\n"+message);err!=nil{fmt.Fprintln(out,"Warning: notification queue unavailable");return}
 if err:=flushNotices(audit,db,account,settings,3);err!=nil{fmt.Fprintln(out,"Warning: Telegram delivery incomplete; inspect notify list")}
}
func flushNotices(ctx context.Context,db *storage.Store,account string,settings config.Telegram,limit int)error{
 if !settings.Enabled{return nil};client:=notify.New(settings.Token,settings.ChatID,settings.Timeout)
 for i:=0;i<limit;i++{
  n,found,err:=db.ClaimNotice(ctx,account,settings.ChatID);if err!=nil{return err};if !found{return nil}
  err=client.Send(ctx,n.Message);state,message,delay:="sent","",time.Duration(0)
  if err!=nil{state="unknown";message=err.Error();var failure *notify.Failure;if errors.As(err,&failure)&&failure.Rejected{state="rejected";if failure.RetryAfter>0&&n.Attempts<3{state="queued";delay=failure.RetryAfter}}}
  audit,cancel:=context.WithTimeout(context.WithoutCancel(ctx),5*time.Second);saveErr:=db.FinishNotice(audit,n.ID,state,message,delay);cancel();if saveErr!=nil{return saveErr};if err!=nil{return err}
 };return nil
}
func runNotify(ctx context.Context,c config.Config,args []string,out io.Writer)error{
 if len(args)!=1||(args[0]!="test"&&args[0]!="flush"&&args[0]!="list"){return fmt.Errorf("use notify test|flush|list")}
 db,err:=tradingDB(ctx,c);if err!=nil{return err};defer db.Close()
 if args[0]=="list"{data,err:=db.NoticeHistory(ctx,c.AccountID);if err!=nil{return err};return printJSON(out,data)}
 settings,err:=config.LoadTelegram();if err!=nil{return err};if !settings.Enabled{return fmt.Errorf("set TELEGRAM_ENABLED=true, TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID in local .env")}
 if args[0]=="test"{id,err:=storage.UUID();if err!=nil{return err};if err:=db.EnqueueNotice(ctx,c.AccountID,"test:"+id,settings.ChatID,"AI Trader · Sandbox\nПроверка уведомлений. Торговые операции не выполнялись.");err!=nil{return err}}
 if err:=flushNotices(ctx,db,c.AccountID,settings,10);err!=nil{return err};return printJSON(out,map[string]any{"status":"notification_queue_processed"})
}

func orderNotice(ctx context.Context,db *storage.Store,account string,intent execution.Intent,out io.Writer){
 if intent.ID==""{return}
 key:=fmt.Sprintf("order:%s:%s:%d:%d",intent.ID,intent.State,intent.ExecutedLots,intent.CommissionNano)
 queueNotice(ctx,db,account,key,fmt.Sprintf("Заявка %s: %s · %s %s · исполнено %d лот(ов)",intent.ID,intent.State,intent.Decision.Action,intent.Decision.Ticker,intent.ExecutedLots),out)
}
