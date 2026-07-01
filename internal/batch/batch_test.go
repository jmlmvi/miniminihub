package batch

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestResolvePrompt(t *testing.T) {
	at := time.Date(2026, 7, 1, 19, 3, 0, 0, time.UTC)
	out, err := ResolvePrompt("Rapport du {{.date}} pour {{.cible}}",
		map[string]string{"date": "{{today}}", "cible": "odhprod"}, at)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Rapport du 2026-07-01 pour odhprod" {
		t.Fatalf("prompt résolu inattendu: %q", out)
	}
}

func TestResolvePromptDefaultDate(t *testing.T) {
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	out, _ := ResolvePrompt("{{.date}}", nil, at)
	if out != "2026-07-01" {
		t.Fatalf("date par défaut: %q", out)
	}
}

// Runner réel (pas de mock) : un script shell qui émet du Markdown sur stdout.
func TestCustomExecRunnerSuccess(t *testing.T) {
	r := CustomExecRunner{}
	res := r.Run(context.Background(), "ignoré",
		map[string]string{"command": "printf '# Rapport\\nOK'"}, 10*time.Second)
	if res.Status != "success" {
		t.Fatalf("status=%s err=%s", res.Status, res.Error)
	}
	if !strings.HasPrefix(string(res.ReportMD), "# Rapport") {
		t.Fatalf("markdown inattendu: %q", res.ReportMD)
	}
}

func TestCustomExecRunnerReadsPromptOnStdin(t *testing.T) {
	r := CustomExecRunner{}
	// `cat` renvoie le prompt reçu sur stdin → prouve le passage du prompt.
	res := r.Run(context.Background(), "# prompt-sur-stdin",
		map[string]string{"command": "cat"}, 10*time.Second)
	if res.Status != "success" || string(res.ReportMD) != "# prompt-sur-stdin" {
		t.Fatalf("stdin non transmis: status=%s md=%q", res.Status, res.ReportMD)
	}
}

func TestCustomExecRunnerMissingCommand(t *testing.T) {
	res := CustomExecRunner{}.Run(context.Background(), "x", map[string]string{}, time.Second)
	if res.Status != "failed" {
		t.Fatalf("command manquante devrait échouer, status=%s", res.Status)
	}
}

func TestCustomExecRunnerTimeout(t *testing.T) {
	res := CustomExecRunner{}.Run(context.Background(), "x",
		map[string]string{"command": "sleep 5"}, 200*time.Millisecond)
	if res.Status != "timeout" {
		t.Fatalf("devrait timeout, status=%s", res.Status)
	}
}

func TestDefaultRunnersRegistry(t *testing.T) {
	r := DefaultRunners()
	for _, k := range []string{"claude_code", "custom_exec", "custom_http", "agentia"} {
		if r[k] == nil {
			t.Errorf("runner %q absent du registry", k)
		}
	}
}
