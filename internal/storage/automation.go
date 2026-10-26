package storage

import (
 "context"
 "fmt"
 "time"

 "ai-trader/internal/execution"
)

// AutomaticCandidates excludes manual analyses, cache reuse and every intent
// that has already reached a submission or terminal state. Prepared intents may
// be recovered after a crash, through a new preflight and the same request UUID.
func(s *Store)AutomaticCandidates(ctx context.Context,account string)([]string,error){
 ctx,cancel:=context.WithTimeout(ctx,10*time.Second);defer cancel()
 rows,err:=s.db.QueryContext(ctx,`SELECT a.id::text FROM analysis_runs a
 LEFT JOIN order_intents i ON i.analysis_id=a.id
 WHERE a.account_id=$1 AND a.automatic_execution AND a.status='validated_proposal'
 AND a.provider IN ('codex_cli','openai_api')
 AND a.result->'decision'->>'action' IN ('BUY','SELL')
 AND a.result->'risk'->>'allowed'='true'
 AND a.created_at>=now()-($2 * interval '1 second') AND a.created_at<=now()
 AND (i.id IS NULL OR i.state='prepared') ORDER BY a.created_at,a.id`,account,execution.MaxAnalysisAge.Seconds())
 if err!=nil{return nil,fmt.Errorf("read automatic execution candidates; run db migrate")};defer rows.Close()
 ids:=[]string{}
 for rows.Next(){var id string;if err:=rows.Scan(&id);err!=nil{return nil,err};ids=append(ids,id)}
 return ids,rows.Err()
}
