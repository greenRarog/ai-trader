//go:build linux

package codexcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-trader/internal/analysis"
)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func fakeCLI(t *testing.T, final, events, ending string) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex")
	body := `#!/bin/sh
set -eu
test -z "${TINVEST_TOKEN+x}"
test -z "${POSTGRES_PASSWORD+x}"
test -z "${DATABASE_URL+x}"
test -z "${OPENAI_API_KEY+x}"
test -z "${CODEX_API_KEY+x}"
test "$1" = exec
shift
sandbox=''
output=''
schema=''
ignore_config=''
instructions=''
while [ "$#" -gt 0 ]; do
 case "$1" in
  --output-last-message) output="$2"; shift 2 ;;
  --output-schema) schema="$2"; shift 2 ;;
  --sandbox) sandbox="$2"; shift 2 ;;
  --ignore-user-config) ignore_config=yes; shift ;;
  -c) case "$2" in model_instructions_file=*) instructions="${2#*=}"; instructions="${instructions#\"}"; instructions="${instructions%\"}" ;; esac; shift 2 ;;
  --dangerously-bypass-approvals-and-sandbox) exit 19 ;;
  *) shift ;;
 esac
done
test "$sandbox" = read-only
test "$ignore_config" = yes
test -f "$schema"
test -f "$instructions"
grep -q "T-Invest Sandbox" "$instructions"
cat "$instructions" > ` + shellQuote(filepath.Join(dir, "instructions.txt")) + `
case "$PWD" in */ai-trader-codex-*) ;; *) exit 20 ;; esac
cat > ` + shellQuote(filepath.Join(dir, "prompt.txt")) + "\n" +
		"printf '%s' " + shellQuote(final) + " > \"$output\"\n" +
		"printf '%s\\n' " + shellQuote(events) + "\n" + ending + "\n"
	if err := os.WriteFile(path, []byte(body), 0755); err != nil { t.Fatal(err) }
	return New(path, "", time.Second), dir
}

const hold = `{"action":"HOLD","ticker":"","class_code":"","lots":0,"reason":"Нет свежих данных"}`
const eventsOK = `{"type":"thread.started","thread_id":"thread-test"}
{"type":"item.completed","item":{"type":"reasoning","text":"PRIVATE_REASONING_MARKER"}}
{"type":"turn.completed","usage":{"input_tokens":14,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":1}}`

func TestCLIInvocationAndEnvironment(t *testing.T) {
	for _, key := range []string{"TINVEST_TOKEN", "POSTGRES_PASSWORD", "DATABASE_URL", "OPENAI_API_KEY", "CODEX_API_KEY"} { t.Setenv(key, "test-secret") }
	client, dir := fakeCLI(t, hold, eventsOK, "exit 0")
	request, err := BuildRequest("", analysis.Snapshot{Mode: "experimental"})
	if err != nil { t.Fatal(err) }
	result, err := client.Respond(context.Background(), request)
	if err != nil { t.Fatal(err) }
	if result.Provider != "codex_cli" || result.ResponseID != "thread-test" || result.Decision == nil || result.Decision.Action != "HOLD" { t.Fatalf("bad result: %+v", result) }
	if result.Usage == nil || result.Usage.TotalTokens != 17 || result.Usage.InputDetails.CachedTokens != 4 { t.Fatal("usage missing") }
	if strings.Contains(string(result.Raw), "PRIVATE_REASONING_MARKER") { t.Fatal("private reasoning was persisted") }
	prompt, err := os.ReadFile(filepath.Join(dir, "prompt.txt")); if err != nil { t.Fatal(err) }
	if strings.Contains(string(prompt), "test-secret") || strings.Contains(string(prompt), "You are an analysis-only agent") || !strings.Contains(string(prompt), "experimental") { t.Fatal("wrong prompt") }
}

func TestCLIErrorHandling(t *testing.T) {
	for _, tt := range []struct { name, final, events, ending string }{
		{"bad decision", `{}`, eventsOK, "exit 0"},
		{"missing final", "", eventsOK, "exit 0"},
		{"missing completion", hold, `{"type":"thread.started","thread_id":"test"}`, "exit 0"},
		{"bad events", hold, "not json", "exit 0"},
		{"failed turn", hold, `{"type":"turn.failed"}`, "exit 0"},
		{"nonzero exit", hold, eventsOK, "echo stderr-secret >&2; exit 1"},
		{"tool activity", hold, "{\"type\":\"item.completed\",\"item\":{\"type\":\"command_execution\"}}\n" + eventsOK, "exit 0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := fakeCLI(t, tt.final, tt.events, tt.ending)
			request, _ := BuildRequest("", analysis.Snapshot{})
			result, err := client.Respond(context.Background(), request)
			if err == nil { t.Fatal("accepted failed run") }
			if strings.Contains(err.Error(), "stderr-secret") || strings.Contains(string(result.Raw), "stderr-secret") { t.Fatal("stderr leaked") }
		})
	}
}

func TestCLITimeoutKillsProcessGroup(t *testing.T) {
	client, _ := fakeCLI(t, hold, eventsOK, "sleep 30")
	client.Timeout = 50*time.Millisecond
	request, _ := BuildRequest("", analysis.Snapshot{})
	started := time.Now()
	_, err := client.Respond(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) { t.Fatalf("expected timeout: %v", err) }
	if time.Since(started) > 3*time.Second { t.Fatal("process group did not stop promptly") }
}

func TestEnvironmentAllowlistAndOutputBound(t *testing.T) {
	env := childEnvironment([]string{"HOME=/home/test", "CODEX_HOME=/auth", "PATH=/bin", "HTTPS_PROXY=https://proxy", "TINVEST_TOKEN=secret", "DATABASE_URL=secret", "OPENAI_API_KEY=secret"})
	if len(env) != 4 { t.Fatalf("unexpected environment: %v", env) }
	buffer := &limitedBuffer{max: 3}
	if n, err := buffer.Write([]byte("abcdef")); n != 6 || err != nil { t.Fatal("wrong write result") }
	if string(buffer.Bytes()) != "abc" || !buffer.exceeded { t.Fatal("output not bounded") }
	encoded, _ := BuildRequest("model-test", analysis.Snapshot{})
	var r request
	if err := json.Unmarshal(encoded, &r); err != nil || r.Model != "model-test" || r.Provider != "codex_cli" { t.Fatal("wrong audit request") }
}
