package codexcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
 "strconv"
	"strings"
	"time"

	"ai-trader/internal/analysis"
)

const maxLogBytes = 4 * 1024 * 1024
const maxDecisionBytes = 64 * 1024

type Client struct {
	Binary string
	Model string
	Timeout time.Duration
}

func New(binary, model string, timeout time.Duration) *Client {
	return &Client{Binary: binary, Model: model, Timeout: timeout}
}

type request struct {
	Provider string `json:"provider"`
	Model string `json:"model,omitempty"`
	Instructions string `json:"instructions"`
	Input analysis.ModelContext `json:"input"`
	Schema map[string]any `json:"output_schema"`
}

func BuildRequest(model string, snapshot analysis.Snapshot) (json.RawMessage, error) {
	compact,err:=analysis.Compact(snapshot);if err!=nil{return nil,err}
 return json.Marshal(request{Provider: "codex_cli", Model: model,
		Instructions: analysis.Instructions + "\nDo not use any tools, inspect files, run commands, or delegate. Return only the requested decision JSON.", Input: compact, Schema: analysis.DecisionSchema()})
}

// Respond starts one isolated CLI invocation using the existing ChatGPT login.
// No auth files are copied and no broker/database credentials are inherited.
func (c *Client) Respond(ctx context.Context, data json.RawMessage) (analysis.Result, error) {
	result := analysis.Result{Provider: "codex_cli", Model: c.Model}
	if result.Model == "" { result.Model = "codex-default" }
	var input request
	if err := json.Unmarshal(data, &input); err != nil || input.Provider != "codex_cli" || input.Model != c.Model {
		return result, fmt.Errorf("invalid Codex request")
	}
	binary, err := exec.LookPath(c.Binary)
	if err != nil { return result, fmt.Errorf("Codex CLI not found; run the local launcher on the host where codex login works") }
	binary, err = filepath.Abs(binary)
	if err != nil { return result, fmt.Errorf("resolve Codex executable") }
	dir, err := os.MkdirTemp("", "ai-trader-codex-")
	if err != nil { return result, fmt.Errorf("create temporary Codex workspace") }
	defer os.RemoveAll(dir)
	schemaPath, outputPath := filepath.Join(dir, "decision.schema.json"), filepath.Join(dir, "decision.json")
	schema, err := json.Marshal(input.Schema)
	if err != nil { return result, fmt.Errorf("encode decision schema") }
	if err := os.WriteFile(schemaPath, schema, 0600); err != nil { return result, fmt.Errorf("write decision schema") }
	if err := os.WriteFile(outputPath, nil, 0600); err != nil { return result, fmt.Errorf("prepare decision output") }
	instructionsPath:=filepath.Join(dir,"analysis.instructions.txt")
 if err:=os.WriteFile(instructionsPath,[]byte(input.Instructions),0600);err!=nil{return result,fmt.Errorf("write analysis instructions")}
 market, err := json.Marshal(input.Input)
	if err != nil { return result, fmt.Errorf("encode market context") }
	prompt := "Market context:\n" + string(market)
	args := []string{"exec", "--ignore-user-config", "--ephemeral", "--skip-git-repo-check",
		"--sandbox", "read-only", "--json", "--color", "never",
		"--output-schema", schemaPath, "--output-last-message", outputPath,
		"-c", `approval_policy="never"`, "-c", `forced_login_method="chatgpt"`,
		"-c", `web_search="disabled"`, "-c", "project_doc_max_bytes=0",
  "-c", "model_instructions_file="+strconv.Quote(instructionsPath),
  "-c", `developer_instructions=""`}
	// Supported by CLI 0.164.0. Keep local hooks, plugins and execution tools out
	// of an analysis run; read-only sandbox is an additional restriction.
	for _, feature := range []string{"shell_tool", "apps", "plugins", "hooks", "browser_use", "computer_use", "image_generation", "multi_agent"} {
		args = append(args, "-c", "features."+feature+"=false")
	}
	if c.Model != "" { args = append(args, "--model", c.Model) }
	args = append(args, "-")
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = childEnvironment(os.Environ())
	cmd.Stdin = strings.NewReader(prompt)
	stdout, stderr := &limitedBuffer{max: maxLogBytes}, &limitedBuffer{max: 64 * 1024}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	configureProcess(cmd)
	runErr := cmd.Run()
	completed, toolActivity, eventsErr := readEvents(stdout.Bytes(), &result)
	decisionText, readErr := readDecision(outputPath)
	// Store final text and audit metadata only, never private reasoning or CLI
	// stderr, which may contain authentication/session diagnostics.
	result.Raw, _ = json.Marshal(struct {
		Output string `json:"output_text"`
		ThreadID string `json:"thread_id,omitempty"`
		Usage *analysis.Usage `json:"usage,omitempty"`
		ToolActivity bool `json:"tool_activity"`
	}{string(decisionText), result.ResponseID, result.Usage, toolActivity})
	if ctx.Err() != nil { return result, fmt.Errorf("Codex request cancelled or timed out: %w", ctx.Err()) }
	if runErr != nil { return result, fmt.Errorf("Codex CLI failed; check codex login status, model access and subscription limits (no automatic retry)") }
	if stdout.exceeded || stderr.exceeded { return result, fmt.Errorf("Codex output exceeds size limit") }
	if eventsErr != nil { return result, eventsErr }
	if !completed { return result, fmt.Errorf("Codex did not report a completed turn") }
	if toolActivity { return result, fmt.Errorf("Codex attempted tool activity; proposal rejected") }
	if readErr != nil { return result, readErr }
	result.Decision, err = analysis.DecodeDecision(decisionText)
	return result, err
}

func childEnvironment(environment []string) []string {
	allowed := map[string]bool{}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "PATH", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "LANG", "LC_ALL", "TERM", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		allowed[key] = true
	}
	filtered := []string{}
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[key] { filtered = append(filtered, entry) }
	}
	return filtered
}

type limitedBuffer struct {
	data []byte
	max int
	exceeded bool
}
func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.max - len(b.data)
	if len(p) > remaining { p = p[:remaining]; b.exceeded = true }
	b.data = append(b.data, p...)
	return n, nil
}
func (b *limitedBuffer) Bytes() []byte { return b.data }

func readDecision(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil { return nil, fmt.Errorf("Codex final response is missing") }
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDecisionBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxDecisionBytes { return nil, fmt.Errorf("Codex final response is empty or too large") }
	return data, nil
}

func readEvents(data []byte, result *analysis.Result) (completed, toolActivity bool, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxLogBytes)
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
			ThreadID string `json:"thread_id"`
			Usage *struct {
				Input int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Reasoning int64 `json:"reasoning_output_tokens"`
			} `json:"usage"`
			Item struct { Type string `json:"type"` } `json:"item"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil { return false, toolActivity, fmt.Errorf("invalid Codex JSONL event") }
		switch event.Type {
		case "thread.started":
			result.ResponseID = event.ThreadID
		case "turn.completed":
			completed = true
			if u := event.Usage; u != nil {
				if u.Input < 0 || u.Output < 0 || u.Input > 1000000000 || u.Output > 1000000000 { return false, toolActivity, fmt.Errorf("invalid Codex usage") }
				result.Usage = &analysis.Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Input + u.Output}
				result.Usage.InputDetails.CachedTokens = u.Cached
				result.Usage.OutputDetails.ReasoningTokens = u.Reasoning
			}
		case "item.started", "item.completed":
			if event.Item.Type != "" && event.Item.Type != "agent_message" && event.Item.Type != "reasoning" { toolActivity = true }
		case "turn.failed", "error":
			return false, toolActivity, fmt.Errorf("Codex reported a failed turn")
		}
	}
	if err := scanner.Err(); err != nil { return false, toolActivity, fmt.Errorf("Codex JSONL event exceeds size limit") }
	return completed, toolActivity, nil
}
