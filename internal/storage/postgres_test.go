package storage

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"ai-trader/internal/analysis"
)

// This test is opt-in; Compose supplies a separate disposable PostgreSQL.
func TestPostgresLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" { t.Skip("set TEST_DATABASE_URL or run docker compose --profile test run --rm tests") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second); defer cancel()
	db, err := Open(ctx, dsn); if err != nil { t.Fatal(err) }
	t.Cleanup(func() { db.Close() })
	for i := 0; i < 2; i++ { if err := db.Migrate(ctx); err != nil { t.Fatal(err) } }
	id, err := db.Begin(ctx, "test-account", "openai_api", "test-model", "test-v1")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second); defer cancel()
		if _, err := db.db.ExecContext(cleanup, "DELETE FROM analysis_runs WHERE id=$1", id); err != nil { t.Error("cleanup test record failed") }
	})
	snapshot := analysis.Snapshot{CapturedAt: time.Now().UTC(), Mode: "protective", Positions: json.RawMessage(`{"money":[]}`), Portfolio: json.RawMessage(`{}`)}
	request := json.RawMessage(`{"model":"test-model","store":false}`)
	if err := db.Prepare(ctx, id, snapshot, request); err != nil { t.Fatal(err) }
	if err := db.Prepare(ctx, id, snapshot, request); err == nil { t.Fatal("allowed duplicate state transition") }
	result := analysis.Result{Decision: &analysis.Decision{Action: "HOLD", Reason: "Недостаточно данных"}, Usage: &analysis.Usage{TotalTokens: 42}}
	if err := db.Finish(ctx, id, "validated_proposal", result, ""); err != nil { t.Fatal(err) }
	if err := db.Finish(ctx, id, "model_error", result, "overwrite"); err == nil { t.Fatal("overwrote terminal state") }
	data, err := db.History(ctx, id, 1); if err != nil { t.Fatal(err) }
	var rows []struct {
		ID string `json:"id"`
		Status string `json:"status"`
		Provider string `json:"provider"`
		Request json.RawMessage `json:"request"`
		Snapshot analysis.Snapshot `json:"snapshot"`
		Result analysis.Result `json:"result"`
		ExecutionEnabled bool `json:"execution_enabled"`
	}
	if err := json.Unmarshal(data, &rows); err != nil { t.Fatal(err) }
	if len(rows) != 1 || rows[0].ID != id || rows[0].Status != "validated_proposal" || rows[0].ExecutionEnabled || rows[0].Provider != "openai_api" { t.Fatalf("wrong history: %s", data) }
	if rows[0].Result.Usage == nil || rows[0].Result.Usage.TotalTokens != 42 || rows[0].Snapshot.Mode != "protective" || !json.Valid(rows[0].Request) { t.Fatal("history lost context or usage") }
	if _, err := db.History(ctx, "", 10); err != nil { t.Fatal(err) }
	if _, err := db.db.ExecContext(ctx, "UPDATE analysis_runs SET execution_enabled=true WHERE id=$1", id); err == nil { t.Fatal("database allowed execution flag") }
}

func TestEquityMetricsAndMoscowDayBoundary(t *testing.T){
 dsn:=os.Getenv("TEST_DATABASE_URL");if dsn==""{t.Skip("requires disposable PostgreSQL")}
 ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
 db,err:=Open(ctx,dsn);if err!=nil{t.Fatal(err)};t.Cleanup(func(){db.Close()})
 if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)}
 run,err:=db.Begin(ctx,"equity-test","manual_check","test","test");if err!=nil{t.Fatal(err)}
 account:="equity-test-"+run
 t.Cleanup(func(){cleanup,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();db.db.ExecContext(cleanup,"DELETE FROM equity_samples WHERE account_id=$1",account);db.db.ExecContext(cleanup,"DELETE FROM analysis_runs WHERE id=$1",run)})
 before:=time.Date(2026,10,26,20,59,0,0,time.UTC)
 first,err:=db.RecordEquity(ctx,account,run,before,100000000000000);if err!=nil{t.Fatal(err)}
 if first.DailyChangeBPS!=0||first.Samples!=1{t.Fatalf("bad first reference: %+v",first)}
 again,err:=db.RecordEquity(ctx,account,run,before,100000000000000);if err!=nil||again.Samples!=1{t.Fatalf("not idempotent: %+v %v",again,err)}
 if _,err:=db.RecordEquity(ctx,account,run,before,99000000000000);err==nil{t.Fatal("accepted conflicting sample")}
 last,err:=db.RecordEquity(ctx,account,"",before.Add(30*time.Second),97000000000000);if err!=nil{t.Fatal(err)}
 if last.DailyChangeBPS!=-300||last.DrawdownBPS!=300||!last.DailyHalt{t.Fatalf("wrong loss: %+v",last)}
 recovered,err:=db.RecordEquity(ctx,account,"",before.Add(40*time.Second),99000000000000);if err!=nil||!recovered.DailyHalt{t.Fatalf("daily stop did not latch: %+v %v",recovered,err)}
 next,err:=db.RecordEquity(ctx,account,"",before.Add(2*time.Minute),96000000000000);if err!=nil{t.Fatal(err)}
 if next.DailyChangeBPS!=0||next.DrawdownBPS!=400||next.DailyHalt||next.DayStartNano!="96000000000000"{t.Fatalf("wrong Moscow rollover: %+v",next)}
 hit,err:=db.RecordEquity(ctx,account,"",before.Add(3*time.Minute),85000000000000);if err!=nil||!hit.DrawdownHalt||hit.MaxDrawdownBPS!=1500{t.Fatalf("drawdown did not trigger: %+v %v",hit,err)}
 recovered,err=db.RecordEquity(ctx,account,"",before.Add(4*time.Minute),99000000000000);if err!=nil||!recovered.DrawdownHalt{t.Fatalf("drawdown stop did not latch: %+v %v",recovered,err)}
 if _,err:=db.EquityHistory(ctx,account,10);err!=nil{t.Fatal(err)}
}
