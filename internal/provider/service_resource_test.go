// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import "testing"

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
