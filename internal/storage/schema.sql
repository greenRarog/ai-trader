CREATE TABLE IF NOT EXISTS analysis_runs (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status text NOT NULL CHECK (status IN ('collecting','requesting','validated_proposal','rejected','broker_error','model_error')),
    account_id text NOT NULL,
    model text NOT NULL,
    prompt_version text NOT NULL,
    snapshot jsonb,
    request jsonb,
    result jsonb,
    error text NOT NULL DEFAULT '',
    execution_enabled boolean NOT NULL DEFAULT false CHECK (execution_enabled = false)
);
CREATE INDEX IF NOT EXISTS analysis_runs_created_at_idx ON analysis_runs (created_at DESC, id);
CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
INSERT INTO schema_migrations(version) VALUES (1) ON CONFLICT DO NOTHING;

-- v2: identify the backend before a model request starts, preserving v1 history.
ALTER TABLE analysis_runs ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT 'openai_api';
INSERT INTO schema_migrations(version) VALUES (2) ON CONFLICT DO NOTHING;

-- v3: sampled portfolio equity, independent of model decisions.
CREATE TABLE IF NOT EXISTS equity_samples (
 id bigserial PRIMARY KEY,
 account_id text NOT NULL,
 run_id uuid UNIQUE REFERENCES analysis_runs(id) ON DELETE CASCADE,
 observed_at timestamptz NOT NULL,
 equity_nano numeric(30,0) NOT NULL CHECK (equity_nano >= 0)
);
CREATE INDEX IF NOT EXISTS equity_samples_account_time_idx ON equity_samples(account_id,observed_at,id);
INSERT INTO schema_migrations(version) VALUES (3) ON CONFLICT DO NOTHING;

-- v4: Sandbox execution and explicit external cash flows.
CREATE TABLE IF NOT EXISTS trading_accounts (
 account_id text PRIMARY KEY,
 paused boolean NOT NULL DEFAULT true,
 pause_reason text NOT NULL DEFAULT 'not enabled',
 anchor_cash_nano numeric(30,0),
 anchor_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cashflows (
 id uuid PRIMARY KEY,
 account_id text NOT NULL,
 amount_nano numeric(30,0) NOT NULL CHECK(amount_nano <> 0),
 occurred_at timestamptz NOT NULL,
 source text NOT NULL CHECK(source IN ('sandbox_payin','owner_record')),
 memo text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cashflows_account_time_idx ON cashflows(account_id,occurred_at);
CREATE TABLE IF NOT EXISTS funding_intents (
 id uuid PRIMARY KEY,
 account_id text NOT NULL,
 amount_nano numeric(30,0) NOT NULL CHECK(amount_nano > 0),
 before_cash_nano numeric(30,0) NOT NULL,
 state text NOT NULL CHECK(state IN ('funding','unknown','confirmed','abandoned')),
 occurred_at timestamptz NOT NULL,
 error text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS order_intents (
 id uuid PRIMARY KEY,
 analysis_id uuid UNIQUE NOT NULL REFERENCES analysis_runs(id),
 account_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('prepared','submitting','acknowledged','unknown','active','partial','filled','cancelled','rejected','blocked')),
 decision jsonb NOT NULL,
 request jsonb,
 snapshot jsonb,
 risk jsonb,
 broker_id text NOT NULL DEFAULT '',
 executed_lots bigint NOT NULL DEFAULT 0 CHECK(executed_lots >= 0),
 cash_delta_nano numeric(30,0) NOT NULL DEFAULT 0,
 commission_nano numeric(30,0) NOT NULL DEFAULT 0,
 broker_state jsonb,
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS order_intents_account_idx ON order_intents(account_id,created_at);
CREATE TABLE IF NOT EXISTS order_events (
 id bigserial PRIMARY KEY,
 intent_id uuid NOT NULL REFERENCES order_intents(id) ON DELETE CASCADE,
 occurred_at timestamptz NOT NULL DEFAULT now(),
 state text NOT NULL,
 payload jsonb NOT NULL
);
CREATE OR REPLACE FUNCTION log_order_intent_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO order_events(intent_id,state,payload) VALUES(NEW.id,NEW.state,to_jsonb(NEW));
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS order_intent_audit ON order_intents;
CREATE TRIGGER order_intent_audit AFTER INSERT OR UPDATE ON order_intents FOR EACH ROW EXECUTE FUNCTION log_order_intent_event();
INSERT INTO schema_migrations(version) VALUES (4) ON CONFLICT DO NOTHING;

-- v5: local gates, bounded model attempts and duplicate-request audit.
ALTER TABLE analysis_runs DROP CONSTRAINT IF EXISTS analysis_runs_status_check;
ALTER TABLE analysis_runs ADD CONSTRAINT analysis_runs_status_check CHECK(status IN
 ('collecting','requesting','validated_proposal','rejected','broker_error','model_error','skipped','reused'));
ALTER TABLE analysis_runs ADD COLUMN IF NOT EXISTS fingerprint text;
ALTER TABLE analysis_runs ADD COLUMN IF NOT EXISTS model_attempted_at timestamptz;
CREATE INDEX IF NOT EXISTS analysis_attempts_idx ON analysis_runs(account_id,model_attempted_at) WHERE model_attempted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS analysis_fingerprint_idx ON analysis_runs(account_id,provider,model,prompt_version,fingerprint,created_at DESC) WHERE model_attempted_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS analysis_slots (
 account_id text NOT NULL,
 slot_key text NOT NULL,
 attempted_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(account_id,slot_key)
);
INSERT INTO schema_migrations(version) VALUES(5) ON CONFLICT DO NOTHING;

-- v6: operational notification outbox; no bot tokens are stored here.
CREATE TABLE IF NOT EXISTS notification_outbox (
 id bigserial PRIMARY KEY,
 account_id text NOT NULL,
 dedupe_key text NOT NULL,
 recipient text NOT NULL,
 message text NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','sending','sent','rejected','unknown')),
 attempts integer NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 error text NOT NULL DEFAULT '',
 UNIQUE(account_id,dedupe_key)
);
CREATE INDEX IF NOT EXISTS notification_pending_idx ON notification_outbox(account_id,next_attempt_at) WHERE state='queued';
INSERT INTO schema_migrations(version) VALUES(6) ON CONFLICT DO NOTHING;

-- v7: durable opt-in provenance for automatic Sandbox execution/recovery.
ALTER TABLE analysis_runs ADD COLUMN IF NOT EXISTS automatic_execution boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS automatic_analysis_idx ON analysis_runs(account_id,created_at) WHERE automatic_execution;
INSERT INTO schema_migrations(version) VALUES(7) ON CONFLICT DO NOTHING;
