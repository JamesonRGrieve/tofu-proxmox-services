// SPDX-License-Identifier: AGPL-3.0-or-later

package ansible

import "testing"

func TestParseRecap(t *testing.T) {
	cases := []struct {
		name                               string
		stdout                             string
		ok, changed, unreach, failed, skip int
	}{
		{
			name: "ok with changes",
			stdout: "PLAY RECAP *********************************************************\n" +
				"10.0.0.5 : ok=12 changed=3 unreachable=0 failed=0 skipped=2 rescued=0 ignored=0\n",
			ok: 12, changed: 3, unreach: 0, failed: 0, skip: 2,
		},
		{
			name: "unreachable",
			stdout: "PLAY RECAP ***\n" +
				"1.2.3.4 : ok=0 changed=0 unreachable=1 failed=0 skipped=0 rescued=0 ignored=0\n",
			ok: 0, changed: 0, unreach: 1, failed: 0, skip: 0,
		},
		{
			name: "failed",
			stdout: "PLAY RECAP ***\n" +
				"h : ok=5 changed=1 unreachable=0 failed=2 skipped=1 rescued=0 ignored=0\n",
			ok: 5, changed: 1, unreach: 0, failed: 2, skip: 1,
		},
		{
			name:   "no recap (early failure)",
			stdout: "ERROR! the playbook could not be found",
			ok:     0, changed: 0, unreach: 0, failed: 0, skip: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ParseRecap(tc.stdout)
			if r.OK != tc.ok || r.Changed != tc.changed || r.Unreachable != tc.unreach || r.Failed != tc.failed || r.Skipped != tc.skip {
				t.Errorf("ParseRecap = ok=%d changed=%d unreachable=%d failed=%d skipped=%d; want ok=%d changed=%d unreachable=%d failed=%d skipped=%d",
					r.OK, r.Changed, r.Unreachable, r.Failed, r.Skipped, tc.ok, tc.changed, tc.unreach, tc.failed, tc.skip)
			}
		})
	}
}
