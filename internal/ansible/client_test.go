// SPDX-License-Identifier: AGPL-3.0-or-later

package ansible

import (
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		in      RunInput
		varsFil string
		want    []string
	}{
		{
			name:    "full: vault + ssh + vars file + sorted extra vars",
			cfg:     Config{DefaultSSHUser: "root", SSHExtraArgs: "-o X", VaultPasswordFile: "/v"},
			in:      RunInput{Playbook: "playbooks/applications/grafana/baremetal/install_grafana.yml", TargetIP: "10.0.0.5", ExtraVars: map[string]string{"b": "2", "a": "1"}},
			varsFil: "/tmp/vars.json",
			want: []string{
				"playbooks/applications/grafana/baremetal/install_grafana.yml",
				"-i", "10.0.0.5,", "-u", "root", "-e", "target_host=10.0.0.5",
				"--vault-password-file", "/v", "--ssh-extra-args", "-o X",
				"-e", "@/tmp/vars.json", "-e", "a=1", "-e", "b=2",
			},
		},
		{
			name:    "minimal: no vault, no vars file, ssh user override, --check",
			cfg:     Config{DefaultSSHUser: "root", SSHExtraArgs: "-o X"},
			in:      RunInput{Playbook: "p.yml", TargetIP: "1.2.3.4", SSHUser: "deploy", Check: true},
			varsFil: "",
			want: []string{
				"p.yml", "-i", "1.2.3.4,", "-u", "deploy", "-e", "target_host=1.2.3.4",
				"--ssh-extra-args", "-o X", "--check",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgs(tc.cfg, tc.in, tc.varsFil)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("buildArgs() =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	if exitCode(nil) != 0 {
		t.Error("nil error -> rc 0")
	}
}
