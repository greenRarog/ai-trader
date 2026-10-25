package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"ai-trader/internal/analysis"
)

const endpoint = "https://api.openai.com/v1/responses"
const PromptVersion = analysis.PromptVersion

type Client struct {
	Key     string
	Timeout time.Duration
	http    *http.Client
}

func New(key string, timeout time.Duration) *Client {
	return &Client{Key: key, Timeout: timeout, http: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func BuildRequest(model string, maxTokens int, snapshot analysis.Snapshot) (json.RawMessage, error) {
	compact,err:=analysis.Compact(snapshot);if err!=nil{return nil,err}
 contextJSON, err := json.Marshal(compact)
	if err != nil { return nil, err }
	return json.Marshal(map[string]any{
		"model": model,
		"store": false,
		"instructions": analysis.Instructions,
		"input": string(contextJSON),
		"max_output_tokens": maxTokens,
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": "trade_proposal", "strict": true,
			"schema": analysis.DecisionSchema(),
		}},
	})
}

// Respond makes exactly one paid attempt. No automatic retry on timeouts or 429.
func (c *Client) Respond(ctx context.Context, request json.RawMessage) (analysis.Result, error) {
	var result analysis.Result
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(request))
	if err != nil { return result, fmt.Errorf("create OpenAI request") }
	req.Header.Set("Authorization", "Bearer " + c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// Never include headers, keys or provider response bodies in errors.
		if ctx.Err() != nil { return result, fmt.Errorf("OpenAI request cancelled or timed out: %w", ctx.Err()) }
		return result, fmt.Errorf("OpenAI network request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil { return result, fmt.Errorf("read OpenAI response failed") }
	if len(body) > 4*1024*1024 { return result, fmt.Errorf("OpenAI response exceeds size limit") }
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("OpenAI HTTP %d (no automatic retry)", resp.StatusCode)
	}
	if !json.Valid(body) { return result, fmt.Errorf("OpenAI returned malformed JSON") }
	result.Raw = json.RawMessage(body)
	var response struct {
		ID string `json:"id"`
		Model string `json:"model"`
		Status string `json:"status"`
		Usage *analysis.Usage `json:"usage"`
		Output []struct {
			Type string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil { return result, fmt.Errorf("invalid OpenAI response envelope") }
	result.ResponseID, result.Model, result.Usage = response.ID, response.Model, response.Usage
	if response.Status != "completed" { return result, fmt.Errorf("OpenAI response not completed") }
	var texts []string
	for _, item := range response.Output {
		if item.Type != "message" { continue }
		for _, content := range item.Content {
			if content.Type == "refusal" { return result, fmt.Errorf("model refused the request") }
			if content.Type == "output_text" { texts = append(texts, content.Text) }
		}
	}
	if len(texts) != 1 { return result, fmt.Errorf("expected one structured decision") }
	result.Decision, err = analysis.DecodeDecision([]byte(texts[0]))
	return result, err
}
