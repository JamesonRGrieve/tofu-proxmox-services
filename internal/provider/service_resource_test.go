// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"testing"

	"github.com/JamesonRGrieve/tofu-proxmox-services/internal/ansible"
)

func TestAppVarsHashStableAcrossOrder(t *testing.T) {
	a := map[string]string{"b": "2", "a": "1", "c": "3"}
	b := map[string]string{"c": "3", "a": "1", "b": "2"}
	if appVarsHash(a) != appVarsHash(b) {
		t.Error("appVarsHash must be independent of map iteration order")
	}
	if appVarsHash(a) == appVarsHash(map[string]string{"a": "1", "b": "2"}) {
		t.Error("different content must hash differently")
	}
	if appVarsHash(nil) != appVarsHash(map[string]string{}) {
		t.Error("nil and empty map should hash equally")
	}
}

func TestSplitServiceID(t *testing.T) {
	cases := []struct {
		id      string
		svc, ip string
		ok      bool
	}{
		{"grafana@192.168.4.40", "grafana", "192.168.4.40", true},
		{"postgresql@10.0.0.5", "postgresql", "10.0.0.5", true},
		{"noatsign", "", "", false},
		{"@1.2.3.4", "", "", false},
		{"svc@", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			svc, ip, ok := splitServiceID(tc.id)
			if ok != tc.ok || svc != tc.svc || ip != tc.ip {
				t.Errorf("splitServiceID(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.id, svc, ip, ok, tc.svc, tc.ip, tc.ok)
			}
		})
	}
}

func TestProvidesTokensSetHash(t *testing.T) {
	mk := func(keys ...string) *ansible.About {
		pt := map[string]string{}
		for _, k := range keys {
			pt[k] = "_ct_url"
		}
		return &ansible.About{ProvidesTokens: pt}
	}
	// Order-independent over the key set.
	if providesTokensSetHash(mk("b", "a")) != providesTokensSetHash(mk("a", "b")) {
		t.Error("hash must be independent of key order")
	}
	// Only the key SET matters, not the values: two Abouts with the same keys but
	// different values hash equally (values are produced by init, not declared).
	same := &ansible.About{ProvidesTokens: map[string]string{"x": "_ct_url"}}
	other := &ansible.About{ProvidesTokens: map[string]string{"x": "x_output_file"}}
	if providesTokensSetHash(same) != providesTokensSetHash(other) {
		t.Error("hash must depend on keys only, not values")
	}
	// A grown set changes the hash — this is what triggers the init-only re-run.
	if providesTokensSetHash(mk("a")) == providesTokensSetHash(mk("a", "b")) {
		t.Error("adding a token must change the hash")
	}
	if providesTokensSetHash(mk()) != providesTokensSetHash(&ansible.About{}) {
		t.Error("empty and nil token sets should hash equally")
	}
}
