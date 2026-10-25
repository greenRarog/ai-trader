package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"ai-trader/internal/analysis"
)

//go:embed schema.sql
var schema string

type Store struct { db *sql.DB }

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil { return nil, fmt.Errorf("invalid PostgreSQL configuration") }
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, connectionError(dsn, err)
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second); defer cancel()
	tx, err := s.db.BeginTx(ctx, nil); if err != nil { return fmt.Errorf("begin database migration") }
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(582134961)"); err != nil { return fmt.Errorf("lock database migration") }
	if _, err = tx.ExecContext(ctx, schema); err != nil { return fmt.Errorf("apply database migration failed") }
	if err = tx.Commit(); err != nil { return fmt.Errorf("commit database migration failed") }
	return nil
}

func (s *Store) Begin(ctx context.Context, account, provider, model, promptVersion string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil { return "", fmt.Errorf("generate analysis ID") }
	b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second); defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO analysis_runs(id,status,account_id,provider,model,prompt_version)
		VALUES ($1,'collecting',$2,$3,$4,$5)`, id, account, provider, model, promptVersion)
	if err != nil { return "", fmt.Errorf("save analysis start failed; run db migrate first") }
	return id, nil
}

func (s *Store) Prepare(ctx context.Context, id string, snapshot analysis.Snapshot, request json.RawMessage) error {
	data, err := json.Marshal(snapshot); if err != nil { return err }
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second); defer cancel()
	res, err := s.db.ExecContext(ctx, `UPDATE analysis_runs SET snapshot=$2::jsonb, request=$3::jsonb,
		status='requesting' WHERE id=$1 AND status='collecting'`, id, string(data), string(request))
	return oneUpdated(res, err)
}

func (s *Store) Finish(ctx context.Context, id, status string, result analysis.Result, message string) error {
	data, err := json.Marshal(result); if err != nil { return err }
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second); defer cancel()
	res, err := s.db.ExecContext(ctx, `UPDATE analysis_runs SET status=$2, result=$3::jsonb,
		error=$4, finished_at=now() WHERE id=$1 AND status IN ('collecting','requesting')`, id, status, string(data), message)
	return oneUpdated(res, err)
}

func oneUpdated(res sql.Result, err error) error {
	if err != nil { return fmt.Errorf("save analysis update failed") }
	n, err := res.RowsAffected(); if err != nil || n != 1 { return fmt.Errorf("analysis state update did not affect exactly one row") }
	return nil
}

func (s *Store) History(ctx context.Context, id string, limit int) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second); defer cancel()
	var rows *sql.Rows
	var err error
	if id != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT row_to_json(r) FROM(SELECT a.*,(SELECT row_to_json(i) FROM order_intents i WHERE i.analysis_id=a.id) AS sandbox_execution FROM analysis_runs a WHERE a.id=$1::uuid) r`, id)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT row_to_json(r) FROM (
			SELECT id,created_at,finished_at,status,provider,model,prompt_version,
			result->'decision' AS decision,result->'usage' AS usage,result->'risk' AS risk,result->'economy' AS economy,model_attempted_at,error,execution_enabled,(SELECT state FROM order_intents WHERE analysis_id=analysis_runs.id) AS sandbox_order_state
			FROM analysis_runs ORDER BY created_at DESC,id LIMIT $1) r`, limit)
	}
	if err != nil { return nil, fmt.Errorf("read analysis history failed; check ID and run db migrate first") }
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil { return nil, fmt.Errorf("decode analysis history failed") }
		items = append(items, json.RawMessage(data))
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("read analysis history interrupted") }
	return json.Marshal(items)
}
