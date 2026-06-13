// SPDX-License-Identifier: AGPL-3.0-or-later

package ansible

import (
	"regexp"
	"strconv"
)

// Result is the parsed outcome of an ansible-playbook run: its return code, the
// summed PLAY RECAP counters, and a tail of stdout for diagnostics.
type Result struct {
	RC          int
	OK          int
	Changed     int
	Unreachable int
	Failed      int
	Skipped     int
	StdoutTail  string
}

// recapLine matches an Ansible PLAY RECAP host line, e.g.:
//
//	host : ok=12 changed=3 unreachable=0 failed=0 skipped=2 rescued=0 ignored=0
var recapLine = regexp.MustCompile(`ok=(\d+)\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)(?:\s+skipped=(\d+))?`)

// ParseRecap extracts and sums the PLAY RECAP counters from ansible-playbook
// stdout (summed across hosts; there is normally one). It never errors — a run
// with no recap (e.g. an early parse failure) yields zeroed counters.
func ParseRecap(stdout string) *Result {
	r := &Result{StdoutTail: tail(stdout, 8192)}
	for _, m := range recapLine.FindAllStringSubmatch(stdout, -1) {
		r.OK += atoi(m[1])
		r.Changed += atoi(m[2])
		r.Unreachable += atoi(m[3])
		r.Failed += atoi(m[4])
		if len(m) > 5 && m[5] != "" {
			r.Skipped += atoi(m[5])
		}
	}
	return r
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
