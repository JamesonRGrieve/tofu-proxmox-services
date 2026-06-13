// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package ansible is the orchestration engine for the proxmox-services provider:
// it shells out to ansible-playbook against a configured Ansible repo to install
// services on Proxmox guests and capture their provides_tokens. It reproduces the
// invocation contract proven by the lab harness (orchestrator/playbook_section.py)
// and has zero terraform dependencies.
package ansible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Config configures a Runner.
type Config struct {
	RepoPath           string        // Ansible repo root; cwd for every invocation
	VaultPasswordFile  string        // optional --vault-password-file
	DefaultSSHUser     string        // default "root"
	DefaultSSHPassword string        // used when a RunInput supplies no SSHPassword
	SSHExtraArgs       string        // default disables host-key + pubkey
	AnsibleBin         string        // default "ansible-playbook" (PATH lookup)
	DefaultTimeout     time.Duration // default 30m
}

// Runner invokes ansible-playbook. Safe for concurrent use (it holds no state).
type Runner struct {
	cfg Config
}

// NewRunner builds a Runner, applying defaults.
func NewRunner(cfg Config) *Runner {
	if cfg.DefaultSSHUser == "" {
		cfg.DefaultSSHUser = "root"
	}
	if cfg.SSHExtraArgs == "" {
		cfg.SSHExtraArgs = "-o StrictHostKeyChecking=no -o PubkeyAuthentication=no"
	}
	if cfg.AnsibleBin == "" {
		cfg.AnsibleBin = "ansible-playbook"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 30 * time.Minute
	}
	return &Runner{cfg: cfg}
}

// RepoPath exposes the configured Ansible repo root (used for LoadAbout).
func (r *Runner) RepoPath() string { return r.cfg.RepoPath }

// RunInput is everything one playbook invocation needs.
type RunInput struct {
	Playbook    string            // path relative to RepoPath
	TargetIP    string            // builds -i "<ip>," and -e target_host=<ip>
	SSHUser     string            // overrides DefaultSSHUser
	SSHPassword string            // injected via the 0600 vars file as ansible_password (never argv)
	AppVars     map[string]string // serialized to a 0600 JSON file -> -e @file
	ExtraVars   map[string]string // flat -e k=v (e.g. token output-file paths)
	Check       bool              // adds --check
	Timeout     time.Duration     // overrides DefaultTimeout
}

// buildArgs assembles the ansible-playbook argv. Pure and deterministic (varsFile
// is the path of an already-written app-vars file, or ""), so it is unit-tested.
func buildArgs(cfg Config, in RunInput, varsFile string) []string {
	user := cfg.DefaultSSHUser
	if in.SSHUser != "" {
		user = in.SSHUser
	}
	args := []string{in.Playbook, "-i", in.TargetIP + ",", "-u", user, "-e", "target_host=" + in.TargetIP}
	if cfg.VaultPasswordFile != "" {
		args = append(args, "--vault-password-file", cfg.VaultPasswordFile)
	}
	if cfg.SSHExtraArgs != "" {
		args = append(args, "--ssh-extra-args", cfg.SSHExtraArgs)
	}
	if varsFile != "" {
		args = append(args, "-e", "@"+varsFile)
	}
	keys := make([]string, 0, len(in.ExtraVars))
	for k := range in.ExtraVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+in.ExtraVars[k])
	}
	if in.Check {
		args = append(args, "--check")
	}
	return args
}

// writeVarsFile writes app_vars (+ ansible_password if set) to a 0600 JSON temp
// file and returns its path (or "" if nothing to write). Secrets stay in the
// file, never on argv.
func writeVarsFile(appVars map[string]string, sshPassword string) (string, error) {
	merged := make(map[string]string, len(appVars)+1)
	for k, v := range appVars {
		merged[k] = v
	}
	if sshPassword != "" {
		merged["ansible_password"] = sshPassword
	}
	if len(merged) == 0 {
		return "", nil
	}
	f, err := os.CreateTemp("", "proxmoxsvc-vars-*.json") // 0600 by default
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(merged); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// RunPlaybook runs one ansible-playbook invocation and returns the parsed recap.
// A non-zero return code is an error (rc=4 UNREACHABLE is surfaced distinctly).
func (r *Runner) RunPlaybook(ctx context.Context, in RunInput) (*Result, error) {
	pw := in.SSHPassword
	if pw == "" {
		pw = r.cfg.DefaultSSHPassword
	}
	varsFile, err := writeVarsFile(in.AppVars, pw)
	if err != nil {
		return nil, fmt.Errorf("write vars file: %w", err)
	}
	if varsFile != "" {
		defer os.Remove(varsFile)
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = r.cfg.DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := buildArgs(r.cfg, in, varsFile)
	cmd := exec.CommandContext(cctx, r.cfg.AnsibleBin, args...)
	cmd.Dir = r.cfg.RepoPath
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()

	res := ParseRecap(buf.String())
	res.RC = exitCode(runErr)

	switch {
	case cctx.Err() == context.DeadlineExceeded:
		return res, fmt.Errorf("ansible-playbook %s on %s timed out after %s", in.Playbook, in.TargetIP, timeout)
	case res.RC == 4:
		return res, fmt.Errorf("ansible-playbook %s on %s: host UNREACHABLE (rc=4)\n%s", in.Playbook, in.TargetIP, res.StdoutTail)
	case res.RC != 0:
		return res, fmt.Errorf("ansible-playbook %s on %s failed (rc=%d)\n%s", in.Playbook, in.TargetIP, res.RC, res.StdoutTail)
	}
	return res, nil
}

// CaptureTokens runs the init playbook (via in.Playbook) wiring each
// provides_tokens entry to a 0600 output file, then reads the files back. The
// "_ct_url" sentinel synthesizes http://<ctIP>:<port> instead of reading a file.
func (r *Runner) CaptureTokens(ctx context.Context, in RunInput, provides map[string]string, ctIP string, port int) (map[string]string, error) {
	tokens := map[string]string{}
	tmpByToken := map[string]string{}
	extra := make(map[string]string, len(in.ExtraVars)+len(provides))
	for k, v := range in.ExtraVars {
		extra[k] = v
	}
	for token, outputVar := range provides {
		if outputVar == "_ct_url" {
			tokens[token] = fmt.Sprintf("http://%s:%d", ctIP, port)
			continue
		}
		f, err := os.CreateTemp("", "proxmoxsvc-tok-*.txt")
		if err != nil {
			return tokens, err
		}
		_ = f.Close()
		tmpByToken[token] = f.Name()
		extra[outputVar] = f.Name()
	}
	defer func() {
		for _, f := range tmpByToken {
			os.Remove(f)
		}
	}()

	in2 := in
	in2.ExtraVars = extra
	if _, err := r.RunPlaybook(ctx, in2); err != nil {
		return tokens, err
	}
	for token, f := range tmpByToken {
		b, err := os.ReadFile(f) //nolint:gosec // path is a temp file we created
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			tokens[token] = v
		}
	}
	return tokens, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
