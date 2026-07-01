// Package batch — V002 P5 : exécution planifiée d'agents IA sur le mmh.
// L'agent est ABSTRAIT (AgentRunner) : le worker ne sait pas comment le rapport
// est produit, seulement qu'il reçoit un prompt et renvoie du Markdown + métriques.
package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RunResult = sortie normalisée d'un run, quel que soit le runner.
type RunResult struct {
	Status    string            // success | failed | timeout
	ReportMD  []byte            // rapport Markdown
	Metrics   map[string]string // durée, coût, tokens, exit_code…
	Error     string
	StartedMs int64
	EndedMs   int64
}

// AgentRunner = contrat commun. `params` vient de JobSpec.agent_params.
type AgentRunner interface {
	Kind() string
	Run(ctx context.Context, prompt string, params map[string]string, timeout time.Duration) RunResult
}

// Registry des runners disponibles (« je veux tout », D2).
func DefaultRunners() map[string]AgentRunner {
	r := map[string]AgentRunner{}
	for _, x := range []AgentRunner{ClaudeCodeRunner{}, CustomExecRunner{}, CustomHttpRunner{}, AgentIARunner{}} {
		r[x.Kind()] = x
	}
	return r
}

func nowMs() int64 { return time.Now().UnixMilli() }

func fail(started int64, msg string) RunResult {
	return RunResult{Status: "failed", Error: msg, StartedMs: started, EndedMs: nowMs(), Metrics: map[string]string{}}
}

// ------------------------------------------------------------------ claude_code
// Exécute `claude -p "<prompt>" --output-format json` dans le cwd du projet
// (hérite du .mcp.json + permissions). Extrait le rapport du champ `result`.
type ClaudeCodeRunner struct{}

func (ClaudeCodeRunner) Kind() string { return "claude_code" }

func (ClaudeCodeRunner) Run(ctx context.Context, prompt string, params map[string]string, timeout time.Duration) RunResult {
	started := nowMs()
	bin := orDefault(params["binary"], "claude")
	args := []string{"-p", prompt, "--output-format", "json"}
	if m := params["model"]; m != "" {
		args = append(args, "--model", m)
	}
	if pm := params["permission_mode"]; pm != "" {
		args = append(args, "--permission-mode", pm)
	}
	if extra := params["extra_args"]; extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	if cwd := params["cwd"]; cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return RunResult{Status: "timeout", Error: "timeout claude", StartedMs: started, EndedMs: nowMs(), Metrics: map[string]string{}}
	}
	if err != nil {
		return fail(started, "claude exit: "+err.Error()+" | "+truncate(stderr.String(), 400))
	}
	// Sortie JSON de claude : { "result": "...md...", "total_cost_usd": .., "num_turns": .., "duration_ms": .. }
	var j struct {
		Result     string  `json:"result"`
		CostUSD    float64 `json:"total_cost_usd"`
		NumTurns   int     `json:"num_turns"`
		DurationMs int64   `json:"duration_ms"`
		IsError    bool    `json:"is_error"`
	}
	md := stdout.Bytes()
	metrics := map[string]string{}
	if json.Unmarshal(stdout.Bytes(), &j) == nil && j.Result != "" {
		md = []byte(j.Result)
		metrics["cost_usd"] = strconv.FormatFloat(j.CostUSD, 'f', 4, 64)
		metrics["num_turns"] = strconv.Itoa(j.NumTurns)
		metrics["duration_ms"] = strconv.FormatInt(j.DurationMs, 10)
		if j.IsError {
			return RunResult{Status: "failed", ReportMD: md, Error: "claude is_error", Metrics: metrics, StartedMs: started, EndedMs: nowMs()}
		}
	}
	return RunResult{Status: "success", ReportMD: md, Metrics: metrics, StartedMs: started, EndedMs: nowMs()}
}

// ------------------------------------------------------------------ custom_exec
// Exécute un binaire/script : prompt sur stdin, Markdown sur stdout.
type CustomExecRunner struct{}

func (CustomExecRunner) Kind() string { return "custom_exec" }

func (CustomExecRunner) Run(ctx context.Context, prompt string, params map[string]string, timeout time.Duration) RunResult {
	started := nowMs()
	binline := params["command"]
	if binline == "" {
		return fail(started, "custom_exec: param 'command' requis")
	}
	// via `sh -c` → parsing shell (guillemets, pipes) ; le prompt arrive sur stdin.
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", binline)
	if cwd := params["cwd"]; cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return RunResult{Status: "timeout", Error: "timeout exec", StartedMs: started, EndedMs: nowMs(), Metrics: map[string]string{}}
	}
	metrics := map[string]string{"exit_code": "0"}
	if err != nil {
		metrics["exit_code"] = err.Error()
		return RunResult{Status: "failed", ReportMD: stdout.Bytes(), Error: "exec: " + err.Error() + " | " + truncate(stderr.String(), 400), Metrics: metrics, StartedMs: started, EndedMs: nowMs()}
	}
	return RunResult{Status: "success", ReportMD: stdout.Bytes(), Metrics: metrics, StartedMs: started, EndedMs: nowMs()}
}

// ------------------------------------------------------------------ custom_http
// POST {prompt} vers un agent maison → réponse {markdown, metrics}.
type CustomHttpRunner struct{}

func (CustomHttpRunner) Kind() string { return "custom_http" }

func (CustomHttpRunner) Run(ctx context.Context, prompt string, params map[string]string, timeout time.Duration) RunResult {
	return httpRun(ctx, prompt, params, timeout, "custom_http")
}

// --------------------------------------------------------------------- agentia
// Variante custom_http pointant l'API AgentIA (raisonnement au Hub/APIM).
type AgentIARunner struct{}

func (AgentIARunner) Kind() string { return "agentia" }

func (AgentIARunner) Run(ctx context.Context, prompt string, params map[string]string, timeout time.Duration) RunResult {
	return httpRun(ctx, prompt, params, timeout, "agentia")
}

func httpRun(ctx context.Context, prompt string, params map[string]string, timeout time.Duration, kind string) RunResult {
	started := nowMs()
	url := params["url"]
	if url == "" {
		return fail(started, kind+": param 'url' requis")
	}
	body, _ := json.Marshal(map[string]string{"prompt": prompt})
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fail(started, kind+": "+err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if h := params["auth_header"]; h != "" {
		if k, v, ok := strings.Cut(h, ":"); ok {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return RunResult{Status: "timeout", Error: "timeout http", StartedMs: started, EndedMs: nowMs(), Metrics: map[string]string{}}
		}
		return fail(started, kind+": "+err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return fail(started, fmt.Sprintf("%s: HTTP %d %s", kind, resp.StatusCode, truncate(string(raw), 300)))
	}
	// réponse { "markdown": "...", "metrics": {..} } ; à défaut, corps brut = markdown.
	var j struct {
		Markdown string            `json:"markdown"`
		Metrics  map[string]string `json:"metrics"`
	}
	md := raw
	metrics := map[string]string{}
	if json.Unmarshal(raw, &j) == nil && j.Markdown != "" {
		md = []byte(j.Markdown)
		if j.Metrics != nil {
			metrics = j.Metrics
		}
	}
	return RunResult{Status: "success", ReportMD: md, Metrics: metrics, StartedMs: started, EndedMs: nowMs()}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
