// Package config charge le bootstrap.json déposé sur la VM au déploiement.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config = contenu de bootstrap.json (sous-ensemble Phase 0).
type Config struct {
	MiniminihubID  string   `json:"miniminihub_id"`
	Slug           string   `json:"slug"`
	ParentEndpoint string   `json:"parent_minihub_endpoint"`
	Mode           string   `json:"mode"`  // persistent | ephemeral
	Roles          []string `json:"roles"` // proxy | smtp | jobs
	HeartbeatMs    int      `json:"heartbeat_ms"`
}

// Load lit et valide le fichier bootstrap.json.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse bootstrap: %w", err)
	}
	if c.ParentEndpoint == "" {
		return nil, fmt.Errorf("parent_minihub_endpoint is required")
	}
	if c.Slug == "" {
		c.Slug = "mmh-unknown"
	}
	if c.HeartbeatMs <= 0 {
		c.HeartbeatMs = 30000
	}
	return &c, nil
}

// HasRole indique si un rôle est activé.
func (c *Config) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}
