package tinvest

import (
 "context"
 "encoding/json"
 "fmt"
 "strconv"
)

type LimitRequest struct {
 AccountID string `json:"accountId"`
 InstrumentID string `json:"instrumentId"`
 Quantity string `json:"quantity"`
 Price Money `json:"price"`
 Direction string `json:"direction"`
 OrderType string `json:"orderType"`
 OrderID string `json:"orderId"`
 TimeInForce string `json:"timeInForce"`
 ConfirmMarginTrade bool `json:"confirmMarginTrade"`
}
type PostOrderResult struct { ID string `json:"orderId"`; Raw json.RawMessage `json:"-"` }
type OrderState struct {
 ID string `json:"orderId"`
 UID string `json:"instrumentUid"`
 Direction string `json:"direction"`
 OrderType string `json:"orderType"`
 Status string `json:"executionReportStatus"`
 LotsRequested string `json:"lotsRequested"`
 LotsExecuted string `json:"lotsExecuted"`
 Total Money `json:"totalOrderAmount"`
 Commission Money `json:"executedCommission"`
 Raw json.RawMessage `json:"-"`
}
func (c *Client) PostLimit(ctx context.Context,input LimitRequest)(PostOrderResult,error){
 var out PostOrderResult
 lots,err:=strconv.ParseInt(input.Quantity,10,64)
 if err!=nil||lots<=0||input.AccountID==""||input.InstrumentID==""||input.OrderID==""||input.OrderType!="ORDER_TYPE_LIMIT"||input.TimeInForce!="TIME_IN_FORCE_FILL_AND_KILL"||input.ConfirmMarginTrade||(input.Direction!="ORDER_DIRECTION_BUY"&&input.Direction!="ORDER_DIRECTION_SELL"){return out,fmt.Errorf("invalid Sandbox limit request")}
 var raw json.RawMessage
 // Mutation: call never retries this POST, regardless of network/API error.
 if err:=c.call(ctx,sandboxService+"PostSandboxOrder",input,&raw,false);err!=nil{return out,err}
 if json.Unmarshal(raw,&out)!=nil||out.ID==""{return out,fmt.Errorf("Sandbox order response has no exchange ID")};out.Raw=raw;return out,nil
}
func(c *Client)OrderState(ctx context.Context,account,id string,requestID bool)(OrderState,error){
 var out OrderState;var raw json.RawMessage
 kind:="ORDER_ID_TYPE_EXCHANGE";if requestID{kind="ORDER_ID_TYPE_REQUEST"}
 if err:=c.call(ctx,sandboxService+"GetSandboxOrderState",map[string]string{"accountId":account,"orderId":id,"orderIdType":kind},&raw,true);err!=nil{return out,err}
 if json.Unmarshal(raw,&out)!=nil||out.ID==""{return out,fmt.Errorf("incomplete Sandbox order state")};out.Raw=raw;return out,nil
}
func(c *Client)CancelSandbox(ctx context.Context,account,id string)error{
 var out json.RawMessage;return c.call(ctx,sandboxService+"CancelSandboxOrder",map[string]string{"accountId":account,"orderId":id},&out,false)
}

// Only explicit gRPC validation/auth failures prove that no order was accepted.
// Other failures (including transport timeout) remain ambiguous.
func DefiniteOrderRejection(err error)bool{
 e,ok:=err.(*apiError);if !ok{return false}
 return (e.status==400&&e.code=="3")||(e.status==401&&e.code=="16")||(e.status==403&&e.code=="7")
}
