// Package execution implements Sandbox-only durable order submission.
package execution

import (
 "context"
 "encoding/json"
 "time"

 "ai-trader/internal/analysis"
 "ai-trader/internal/tinvest"
)

type Intent struct {
 ID string `json:"id"`
 AnalysisID string `json:"analysis_id"`
 AccountID string `json:"account_id"`
 State string `json:"state"`
 Decision analysis.Decision `json:"decision"`
 Request *tinvest.LimitRequest `json:"request,omitempty"`
 Snapshot *analysis.Snapshot `json:"snapshot,omitempty"`
 Risk *analysis.RiskReport `json:"risk,omitempty"`
 BrokerID string `json:"broker_id,omitempty"`
 ExecutedLots int64 `json:"executed_lots"`
 CashDeltaNano int64 `json:"cash_delta_nano"`
 CommissionNano int64 `json:"commission_nano"`
 BrokerState json.RawMessage `json:"broker_state,omitempty"`
 Error string `json:"error"`
 AnalysisCreatedAt time.Time `json:"analysis_created_at"`
 CreatedAt time.Time `json:"created_at"`
 UpdatedAt time.Time `json:"updated_at"`
}
type Prepared struct { Request tinvest.LimitRequest; Snapshot analysis.Snapshot; Risk analysis.RiskReport }
type Store interface {
 GetIntent(context.Context,string)(Intent,error)
 PrepareIntent(context.Context,string,Prepared)error
 MarkSubmitting(context.Context,string)error
 SaveAcknowledgement(context.Context,string,string,json.RawMessage)error
 SaveOrderObservation(context.Context,string,json.RawMessage)error
 SaveOrderState(context.Context,string,string,tinvest.OrderState,int64,int64)error
 MarkOrderError(context.Context,string,string,string)error
}
type Broker interface {
 PostLimit(context.Context,tinvest.LimitRequest)(tinvest.PostOrderResult,error)
 OrderState(context.Context,string,string,bool)(tinvest.OrderState,error)
 CancelSandbox(context.Context,string,string)error
}
type Preflight func(context.Context,Intent)(Prepared,error)
func Terminal(state string)bool{return state=="filled"||state=="cancelled"||state=="rejected"||state=="blocked"}
