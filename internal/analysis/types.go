package analysis

import (
	"encoding/json"
	"time"

	"ai-trader/internal/tinvest"
)

type Quote struct {
 Market *MarketContext `json:"market,omitempty"`
	Instrument tinvest.Instrument `json:"instrument"`
	Price      *tinvest.Money      `json:"price"`
	Time       *time.Time          `json:"time"`
	Usable     bool                `json:"usable"`
	Issue      string              `json:"issue,omitempty"`
}

type Constraints struct {
 CommissionBPS int64 `json:"commission_bps"`
 SlippageBPS int64 `json:"slippage_bps"`
	InitialCapitalRUB int  `json:"initial_capital_rub"`
	MaxPositionRUB    int  `json:"max_position_rub"`
	MaxPositions      int  `json:"max_positions"`
	MaxDailyLossPct   int  `json:"max_daily_loss_pct"`
	MaxDrawdownPct    int  `json:"max_drawdown_pct"`
	LeverageAllowed  bool `json:"leverage_allowed"`
	ShortsAllowed    bool `json:"shorts_allowed"`
	DerivativesAllowed bool `json:"derivatives_allowed"`
}

func DefaultConstraints() Constraints {
	return Constraints{CommissionBPS:5,SlippageBPS:20,InitialCapitalRUB: 100000, MaxPositionRUB: 20000,
		MaxPositions: 5, MaxDailyLossPct: 3, MaxDrawdownPct: 15}
}

type Snapshot struct {
 Account *AccountState `json:"account,omitempty"`
 Equity *EquityMetrics `json:"equity_metrics,omitempty"`
	CapturedAt  time.Time       `json:"captured_at"`
	Mode        string          `json:"instrument_mode"`
	Preferred   []string        `json:"preferred_instruments"`
	Constraints Constraints     `json:"constraints"`
	Positions   json.RawMessage `json:"positions"`
	Portfolio   json.RawMessage `json:"portfolio"`
	Quotes      []Quote          `json:"quotes"`
	Limitations []string         `json:"limitations"`
}

type Decision struct {
	Action    string `json:"action"`
	Ticker    string `json:"ticker"`
	ClassCode string `json:"class_code"`
	Lots      int64  `json:"lots"`
	Reason    string `json:"reason"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	InputDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type Result struct {
 Economy *Economy `json:"economy,omitempty"`
 Risk *RiskReport `json:"risk,omitempty"`
	Provider string `json:"provider,omitempty"`
	ResponseID string          `json:"response_id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	Decision   *Decision       `json:"decision,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
}


type MarketContext struct {
 ObservedAt time.Time `json:"observed_at"`
 Status *tinvest.TradingStatus `json:"trading_status,omitempty"`
 Book *tinvest.OrderBook `json:"order_book,omitempty"`
 Schedules []tinvest.Schedule `json:"schedules"`
 Candles []tinvest.Candle `json:"hourly_candles"`
 Issues []string `json:"issues"`
}
type Holding struct {
 UID string `json:"uid"`
 Ticker string `json:"ticker"`
 Pieces int64 `json:"pieces"`
 FreePieces int64 `json:"free_pieces"`
 ValueNano int64 `json:"value_nano"`
}
type AccountState struct {
 EquityNano int64 `json:"equity_nano"`
 AvailableRUBNano int64 `json:"available_rub_nano"`
 Holdings []Holding `json:"holdings"`
 OpenOrders int `json:"open_orders"`
 Issues []string `json:"issues"`
}
type EquityMetrics struct {
 AdjustedCurrentNano string `json:"adjusted_current_nano,omitempty"`
 FlowAdjustmentNano string `json:"flow_adjustment_nano,omitempty"`
 CashflowAdjusted bool `json:"cashflow_adjusted"`
 DailyHalt bool `json:"daily_halt"`
 DrawdownHalt bool `json:"drawdown_halt"`
 MaxDrawdownBPS int64 `json:"max_drawdown_bps"`
 ObservedAt time.Time `json:"observed_at"`
 CurrentNano string `json:"current_nano"`
 DayStartNano string `json:"day_start_nano"`
 HighWaterNano string `json:"high_water_nano"`
 DayStartAt time.Time `json:"day_start_at"`
 DailyChangeBPS int64 `json:"daily_change_bps"`
 DrawdownBPS int64 `json:"drawdown_bps"`
 Samples int64 `json:"samples"`
 Basis string `json:"basis"`
}
type RiskReport struct {
 Allowed bool `json:"allowed"`
 ExecutionEnabled bool `json:"execution_enabled"`
 Reasons []string `json:"reasons"`
 Pieces int64 `json:"pieces"`
 EstimatedValueNano string `json:"estimated_value_nano,omitempty"`
 EstimatedCommissionNano string `json:"estimated_commission_nano,omitempty"`
 EstimatedCashChangeNano string `json:"estimated_cash_change_nano,omitempty"`
 CommissionBPS int64 `json:"commission_bps"`
 SlippageBPS int64 `json:"slippage_bps"`
}

// Economy measures our payload in bytes, never guesses provider token counts.
// Usage remains the authoritative reported count (including CLI overhead).
type Economy struct {
 ModelAttempted bool `json:"model_attempted"`
 SnapshotBytes int `json:"full_snapshot_bytes"`
 InputBytes int `json:"model_input_bytes"`
 RequestBytes int `json:"request_bytes"`
 ReusedFrom string `json:"reused_from,omitempty"`
 Reasons []string `json:"reasons,omitempty"`
}
type Reservation struct {
 Attempt bool
 Reason string
 SourceID string
 SourceStatus string
 SourceResult Result
}
