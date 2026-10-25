package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"ai-trader/internal/analysis"
)

type roundTripFunc func(*http.Request) (*http.Response, error)
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func completedResponse(decision string) string {
	encoded, _ := json.Marshal(decision)
	return `{"id":"resp-test","model":"test-model","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + string(encoded) + `}]}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":10}}}`
}

func TestRequestAndCompletedResponse(t *testing.T) {
	request, err := BuildRequest("test-model", 2048, analysis.Snapshot{Mode: "protective"})
	if err != nil { t.Fatal(err) }
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(request, &payload); err != nil { t.Fatal(err) }
	if string(payload["store"]) != "false" || string(payload["max_output_tokens"]) != "2048" { t.Fatal("missing privacy or output limit") }
	var text struct { Format struct { Type string `json:"type"`; Strict bool `json:"strict"`; Schema struct { Additional bool `json:"additionalProperties"`; Required []string `json:"required"` } `json:"schema"` } `json:"format"` }
	if err := json.Unmarshal(payload["text"], &text); err != nil { t.Fatal(err) }
	if text.Format.Type != "json_schema" || !text.Format.Strict || text.Format.Schema.Additional || len(text.Format.Schema.Required) != 5 { t.Fatal("schema is not strict") }
	if _, ok := payload["tools"]; ok { t.Fatal("analysis must not expose tools") }
	client := New("fake-key", time.Second)
	calls := 0
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != endpoint || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fake-key" { t.Error("incorrect request") }
		body, _ := io.ReadAll(r.Body)
		if string(body) != string(request) || strings.Contains(string(body), "fake-key") { t.Error("request differs from persisted payload or contains key") }
		return reply(200, completedResponse(`{"action":"HOLD","ticker":"","class_code":"","lots":0,"reason":"Мало данных"}`)), nil
	})
	result, err := client.Respond(context.Background(), request)
	if err != nil { t.Fatal(err) }
	if calls != 1 || result.Decision == nil || result.Decision.Action != "HOLD" || result.ResponseID != "resp-test" { t.Fatalf("bad result: %+v", result) }
	if result.Usage == nil || result.Usage.TotalTokens != 120 || result.Usage.InputDetails.CachedTokens != 10 { t.Fatal("usage not recorded") }
	if !json.Valid(result.Raw) { t.Fatal("missing raw response") }
}

func TestErrorsAreNotRetried(t *testing.T) {
	cases := []struct { name string; status int; body string; raw bool }{
		{"unauthorized", 401, `{"error":{"message":"fake-key"}}`, false},
		{"rate limit", 429, `{"error":{"message":"fake-key"}}`, false},
		{"upstream", 503, `unavailable`, false},
		{"malformed", 200, `not json`, false},
		{"incomplete", 200, `{"id":"resp","status":"incomplete","usage":{"total_tokens":2}}`, true},
		{"refusal", 200, `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal"}]}]}`, true},
		{"invalid decision", 200, completedResponse(`{"action":"HOLD"}`), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			client := New("fake-key", time.Second); calls := 0
			client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return reply(tt.status, tt.body), nil })
			result, err := client.Respond(context.Background(), json.RawMessage(`{}`))
			if err == nil || calls != 1 { t.Fatalf("calls=%d, error=%v", calls, err) }
			if strings.Contains(err.Error(), "fake-key") { t.Fatal("secret leaked") }
			if (result.Raw != nil) != tt.raw { t.Fatal("wrong response preservation") }
		})
	}
}

func TestTimeout(t *testing.T) {
	client := New("fake", 20*time.Millisecond)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	_, err := client.Respond(context.Background(), json.RawMessage(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) { t.Fatalf("expected deadline, got %v", err) }
}
