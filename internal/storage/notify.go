package storage

import (
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "time"
)
type Notice struct{ID int64;Recipient,Message string;Attempts int}
func(s *Store)EnqueueNotice(ctx context.Context,account,key,recipient,message string)error{
 ctx,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
 if account==""||key==""||recipient==""||len([]rune(message))==0||len([]rune(message))>3000{return fmt.Errorf("invalid notification")}
 _,err:=s.db.ExecContext(ctx,"INSERT INTO notification_outbox(account_id,dedupe_key,recipient,message) VALUES($1,$2,$3,$4) ON CONFLICT(account_id,dedupe_key) DO NOTHING",account,key,recipient,message);if err!=nil{return fmt.Errorf("persist notification; run db migrate")};return nil
}
func(s *Store)ClaimNotice(ctx context.Context,account,recipient string)(Notice,bool,error){
 var n Notice;ctx,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
 err:=s.db.QueryRowContext(ctx,`UPDATE notification_outbox SET state='sending',attempts=attempts+1,updated_at=now() WHERE id=(SELECT id FROM notification_outbox WHERE account_id=$1 AND recipient=$2 AND state='queued' AND next_attempt_at<=now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,recipient,message,attempts`,account,recipient).Scan(&n.ID,&n.Recipient,&n.Message,&n.Attempts)
 if err==sql.ErrNoRows{return n,false,nil};if err!=nil{return n,false,fmt.Errorf("claim notification")};return n,true,nil
}
func(s *Store)FinishNotice(ctx context.Context,id int64,state,message string,delay time.Duration)error{
 if state!="sent"&&state!="queued"&&state!="rejected"&&state!="unknown"{return fmt.Errorf("invalid notification state")}
 ctx,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
 res,err:=s.db.ExecContext(ctx,"UPDATE notification_outbox SET state=$2,error=$3,next_attempt_at=now()+($4 * interval '1 second'),updated_at=now() WHERE id=$1 AND state='sending'",id,state,message,delay.Seconds());return oneUpdated(res,err)
}
func(s *Store)NoticeHistory(ctx context.Context,account string)(json.RawMessage,error){
 ctx,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel();var raw []byte
 err:=s.db.QueryRowContext(ctx,`SELECT coalesce(json_agg(r ORDER BY r.id DESC),'[]'::json) FROM(SELECT id,dedupe_key,state,attempts,created_at,updated_at,error FROM notification_outbox WHERE account_id=$1 ORDER BY id DESC LIMIT 100)r`,account).Scan(&raw);if err!=nil{return nil,fmt.Errorf("read notification history")};return raw,nil
}
