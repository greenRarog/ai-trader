package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"ai-trader/internal/policy"
)

// DecodeDecision checks required fields locally too: structured output is not
// a substitute for validation, especially for failed or incomplete responses.
func DecodeDecision(data []byte) (*Decision, error) {
	var wire struct {
		Action    *string `json:"action"`
		Ticker    *string `json:"ticker"`
		ClassCode *string `json:"class_code"`
		Lots      *int64  `json:"lots"`
		Reason    *string `json:"reason"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return nil, fmt.Errorf("invalid decision JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing decision content")
	}
	if wire.Action == nil || wire.Ticker == nil || wire.ClassCode == nil || wire.Lots == nil || wire.Reason == nil {
		return nil, fmt.Errorf("missing or null decision field")
	}
	return &Decision{Action: *wire.Action, Ticker: *wire.Ticker,
		ClassCode: *wire.ClassCode, Lots: *wire.Lots, Reason: *wire.Reason}, nil
}

// Validate checks proposal structure, membership and data freshness only.
// It does not authorize an order or replace the stage-3 Risk Engine.
func Validate(d Decision, s Snapshot, now time.Time, maxAge time.Duration) error {
	if strings.TrimSpace(d.Reason) == "" || len([]rune(d.Reason)) > 4000 {
		return fmt.Errorf("reason must contain 1..4000 characters")
	}
	if d.Action == "HOLD" {
		if d.Ticker != "" || d.ClassCode != "" || d.Lots != 0 {
			return fmt.Errorf("HOLD requires empty ticker/class_code and zero lots")
		}
		return nil
	}
	if d.Action != "BUY" && d.Action != "SELL" {
		return fmt.Errorf("action must be BUY, SELL or HOLD")
	}
	if d.Lots <= 0 || d.Lots > 1000000 {
		return fmt.Errorf("trade proposal requires 1..1000000 whole lots")
	}
	if s.CapturedAt.IsZero() || now.Sub(s.CapturedAt) > maxAge || s.CapturedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("snapshot is stale or has an invalid timestamp")
	}
	for _, q := range s.Quotes {
		i := q.Instrument
		if d.Ticker != i.Ticker || d.ClassCode != i.ClassCode {
			continue
		}
		if err := policy.Check(s.Mode, s.Preferred, i.Ticker, i.ClassCode, i.Kind, i.Currency); err != nil {
			return err
		}
		if !q.Usable || q.Price == nil || q.Time == nil || now.Sub(*q.Time) > maxAge || q.Time.After(now.Add(time.Minute)) {
			return fmt.Errorf("proposal requires a fresh usable quote")
		}
		return nil
	}
	return fmt.Errorf("instrument is outside the supplied market context")
}
