// SPDX-License-Identifier: AGPL-3.0-or-later

package ansible

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// About is the subset of a service's playbooks/applications/<svc>/about.json that
// this provider consumes.
type About struct {
	Name           string            `json:"name"`
	DisplayName    string            `json:"display_name"`
	DefaultPort    int               `json:"default_port"`
	Playbook       string            `json:"playbook"`      // relative to applications/<svc>/, e.g. baremetal/install_grafana.yml
	InitPlaybook   string            `json:"init_playbook"` // relative to repo root when set
	ProvidesTokens map[string]string `json:"provides_tokens"`
	Integrations   []Integration     `json:"integrations"`
	Verification   Verification      `json:"verification"`
}

// Integration describes one service→service wiring declared in about.json.
type Integration struct {
	Name            string   `json:"name"`
	Playbook        string   `json:"playbook"`
	RequiresService string   `json:"requires_service"`
	RequiresTokens  []string `json:"requires_tokens"`
	Description     string   `json:"description"`
}

// Verification holds the health-check contract used for drift detection.
type Verification struct {
	HealthEndpoint    string `json:"health_endpoint"`
	HealthStatusCodes []int  `json:"health_status_codes"`
}

// ParseAbout decodes about.json bytes.
func ParseAbout(b []byte) (*About, error) {
	var a About
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("parse about.json: %w", err)
	}
	return &a, nil
}

// LoadAbout reads playbooks/applications/<service>/about.json under repoPath.
func LoadAbout(repoPath, service string) (*About, error) {
	p := filepath.Join(repoPath, "playbooks", "applications", service, "about.json")
	b, err := os.ReadFile(p) //nolint:gosec // path built from a configured repo + service name
	if err != nil {
		return nil, fmt.Errorf("read about.json for %q: %w", service, err)
	}
	return ParseAbout(b)
}

// InstallPlaybook resolves the install playbook path relative to the repo root.
// about.json's `playbook` is relative to applications/<svc>/.
func (a *About) InstallPlaybook(service string) string {
	if a.Playbook == "" {
		return ""
	}
	return filepath.Join("playbooks", "applications", service, a.Playbook)
}

// InitPlaybookPath resolves the init playbook path relative to the repo root
// (about.json's `init_playbook` is relative to applications/<svc>/, like `playbook`).
func (a *About) InitPlaybookPath(service string) string {
	if a.InitPlaybook == "" {
		return ""
	}
	return filepath.Join("playbooks", "applications", service, a.InitPlaybook)
}

// IntegrationPlaybook returns the playbook path for the integrations[] entry called
// name, or "" when the service declares no such integration. Unlike `playbook` and
// `init_playbook`, integrations[].playbook is already repo-root-relative (repo standard
// §12.1: bare filenames fail because ansible-playbook runs with cwd=ansible_dir).
func (a *About) IntegrationPlaybook(name string) string {
	for _, in := range a.Integrations {
		if in.Name == name {
			return in.Playbook
		}
	}
	return ""
}

// IntegrationTokens returns the token names the integrations[] entry called name
// requires, so a caller can assert every one was supplied before running it.
func (a *About) IntegrationTokens(name string) []string {
	for _, in := range a.Integrations {
		if in.Name == name {
			return in.RequiresTokens
		}
	}
	return nil
}
