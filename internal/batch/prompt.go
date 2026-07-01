package batch

import (
	"bytes"
	"fmt"
	"text/template"
	"time"
)

// ResolvePrompt applique les variables au gabarit (text/template : {{.date}}…).
// Les valeurs de vars peuvent contenir des tokens spéciaux résolus au déclenchement :
//
//	{{today}} → date du jour (YYYY-MM-DD, UTC), {{now}} → RFC3339.
func ResolvePrompt(tmpl string, vars map[string]string, at time.Time) (string, error) {
	data := map[string]string{}
	for k, v := range vars {
		data[k] = resolveTokens(v, at)
	}
	// tokens directs disponibles aussi comme variables.
	if _, ok := data["date"]; !ok {
		data["date"] = at.UTC().Format("2006-01-02")
	}
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New("prompt").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("prompt template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt exec: %w", err)
	}
	return buf.String(), nil
}

func resolveTokens(v string, at time.Time) string {
	switch v {
	case "{{today}}":
		return at.UTC().Format("2006-01-02")
	case "{{now}}":
		return at.UTC().Format(time.RFC3339)
	default:
		return v
	}
}
