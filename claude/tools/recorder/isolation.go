// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// CheckPolicyDirectoryAbsent is a read-only absence check, not launch authority.
// Even an empty directory, dangling symlink or unreadable path is refused. Never
// read, override, rename or delete managed policy to make this check pass.
func CheckPolicyDirectoryAbsent(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.Join(ErrPolicyIsolation, fmt.Errorf("noncanonical policy path"))
	}
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	return errors.Join(ErrPolicyIsolation, fmt.Errorf("managed policy path present or unverifiable"))
}

// ReviewedLinuxCLISHA256 identifies the official 2.1.258 linux-x64 npm artifact.
// The source review in docs/validation/issue-12-policy.md applies only to it.
const ReviewedLinuxCLISHA256 = "704f1334ac65d3e89e1c6c1d7663293ad786a6166afdb71b5075337df630f976"

// Isolation has no public assertion fields. Only InspectIsolation can construct
// usable evidence; every preflight rechecks the actual file/system/workspace.
type Isolation struct {
	cli, digest, endpoint string
	workspace             *Workspace
}

// InspectIsolation supports one reviewed standalone Linux loopback-proxy profile.
// It never starts a CLI, accesses authentication or changes managed policy. Other
// deployments require their own source review, not an assertion flag.
func InspectIsolation(cli, digest, endpoint string, workspace *Workspace) (*Isolation, error) {
	proof := &Isolation{cli: cli, digest: digest, endpoint: endpoint, workspace: workspace}
	if err := proof.validate(); err != nil {
		return nil, err
	}
	return proof, nil
}
func (p *Isolation) validate() error {
	reject := func(reason string) error { return errors.Join(ErrPolicyIsolation, fmt.Errorf("recorder: %s", reason)) }
	if p == nil || p.workspace == nil {
		return reject("independent isolation evidence required")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return reject("unreviewed native platform")
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil || strings.Contains(strings.ToLower(string(kernel)), "microsoft") {
		return reject("unreviewed kernel or WSL policy sources")
	}
	if err := ValidateProxyEndpoint(p.endpoint); err != nil {
		return err
	}
	if p.digest != ReviewedLinuxCLISHA256 {
		return reject("unreviewed native build")
	}
	if err := ValidateCLIBinary(p.cli, p.digest); err != nil {
		return reject("reviewed native build verification failed")
	}
	if err := CheckPolicyDirectoryAbsent("/etc/claude-code"); err != nil {
		return err
	}
	if err := p.workspace.validate(); err != nil {
		return reject("owned isolated workspace required")
	}
	for _, path := range []string{p.workspace.Home, p.workspace.Config, p.workspace.Temp} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			return reject("fresh empty home/config/temp required before launch")
		}
	}
	entries, err := os.ReadDir(p.workspace.Project)
	if err != nil || len(entries) != 1 || entries[0].Name() != "numbers.txt" {
		return reject("exact synthetic project required before launch")
	}
	info, err := os.Lstat(filepath.Join(p.workspace.Project, "numbers.txt"))
	if err != nil || !info.Mode().IsRegular() || info.Size() != 5 {
		return reject("synthetic input is not the expected regular file")
	}
	numbers, err := os.ReadFile(filepath.Join(p.workspace.Project, "numbers.txt"))
	if err != nil || string(numbers) != "7\n11\n" {
		return reject("synthetic input changed")
	}
	return nil
}

// PreflightWithIsolation revalidates non-forgeable evidence separately from the
// approved cost policy. No financial acceptance is treated as policy proof.
func PreflightWithIsolation(policy FinancialPolicy, evidence *Isolation) error {
	if err := evidence.validate(); err != nil {
		return err
	}
	switch policy {
	case AcceptFinancialUncertainty:
		return nil
	case RequireFinancialCap:
		return ErrFinancialCap
	default:
		return fmt.Errorf("recorder: unknown financial policy")
	}
}

// ValidateProxyEndpoint verifies the narrow transport profile; it grants no launch authority.
func ValidateProxyEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return errors.Join(ErrPolicyIsolation, fmt.Errorf("exact loopback proxy endpoint required"))
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Host != "127.0.0.1:"+strconv.Itoa(port) {
		return errors.Join(ErrPolicyIsolation, fmt.Errorf("explicit canonical loopback port required"))
	}
	return nil
}
