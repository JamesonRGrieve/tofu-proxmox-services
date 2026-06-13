// SPDX-License-Identifier: AGPL-3.0-or-later

package ansible

import "testing"

const grafanaAbout = `{
  "name": "grafana",
  "display_name": "Grafana",
  "default_port": 3000,
  "playbook": "baremetal/install_grafana.yml",
  "init_playbook": "",
  "provides_tokens": { "grafana_url": "_ct_url" },
  "integrations": [
    { "name": "alloy", "playbook": "playbooks/applications/grafana/integrate_alloy.yml",
      "requires_service": "alloy", "requires_tokens": ["alloy_url"] }
  ],
  "verification": { "health_endpoint": "/api/health", "health_status_codes": [200] }
}`

func TestParseAbout(t *testing.T) {
	a, err := ParseAbout([]byte(grafanaAbout))
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "grafana" || a.DefaultPort != 3000 {
		t.Errorf("name/port = %q/%d", a.Name, a.DefaultPort)
	}
	if a.ProvidesTokens["grafana_url"] != "_ct_url" {
		t.Errorf("provides_tokens = %v", a.ProvidesTokens)
	}
	if len(a.Integrations) != 1 || a.Integrations[0].RequiresService != "alloy" ||
		len(a.Integrations[0].RequiresTokens) != 1 || a.Integrations[0].RequiresTokens[0] != "alloy_url" {
		t.Errorf("integrations = %+v", a.Integrations)
	}
	if a.Verification.HealthEndpoint != "/api/health" || len(a.Verification.HealthStatusCodes) != 1 || a.Verification.HealthStatusCodes[0] != 200 {
		t.Errorf("verification = %+v", a.Verification)
	}
}

func TestInstallPlaybook(t *testing.T) {
	a := &About{Playbook: "baremetal/install_grafana.yml"}
	want := "playbooks/applications/grafana/baremetal/install_grafana.yml"
	if got := a.InstallPlaybook("grafana"); got != want {
		t.Errorf("InstallPlaybook = %q, want %q", got, want)
	}
	if got := (&About{}).InstallPlaybook("x"); got != "" {
		t.Errorf("empty playbook -> %q, want empty", got)
	}
}
