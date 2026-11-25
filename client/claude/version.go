package claude

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

const MinimumCLIVersion = "2.1.293"

var minimumCLIVersion = semver.MustParse(MinimumCLIVersion)

type CLIVersionError struct {
	Version string
	Minimum string
}

func (e *CLIVersionError) Error() string {
	if e.Version == "" {
		return "claude: could not confirm CLI version; requires stable CLI >= " + e.Minimum
	}
	return fmt.Sprintf("claude: CLI %s is unsupported; requires stable CLI >= %s", e.Version, e.Minimum)
}

func (e *CLIVersionError) Unwrap() error { return ErrUnsupported }

func ValidateCLIVersion(version string) error {
	_, err := parseCLIVersion(version)
	return err
}

func CheckCLIVersion(ctx context.Context, options Options) (string, error) {
	if ctx == nil {
		return "", queue.ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cliPath := options.CLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	var output versionOutput
	command := exec.CommandContext(queryCtx, cliPath, "--version")
	command.Dir, command.Env = options.WorkDir, options.Env
	command.Stdout = &output
	command.WaitDelay = time.Second
	prepareProcess(command)
	command.Cancel = func() error { return terminateProcess(command, true) }
	if err := command.Run(); err != nil {
		return "", &Error{Op: "version", Err: errors.Join(err, queryCtx.Err())}
	}
	if output.truncated {
		return "", &CLIVersionError{Minimum: MinimumCLIVersion}
	}
	version, err := parseCLIVersion(string(output.data))
	if err != nil {
		return "", err
	}
	return version.String(), nil
}

func parseCLIVersion(output string) (*semver.Version, error) {
	value := strings.TrimSpace(output)
	value = strings.TrimSuffix(value, " (Claude Code)")
	value = strings.TrimPrefix(value, "v")
	version, err := semver.StrictNewVersion(value)
	if err != nil {
		return nil, &CLIVersionError{Minimum: MinimumCLIVersion}
	}
	if version.Prerelease() != "" || version.LessThan(minimumCLIVersion) {
		return nil, &CLIVersionError{Version: version.String(), Minimum: MinimumCLIVersion}
	}
	return version, nil
}

type versionOutput struct {
	data      []byte
	truncated bool
}

func (o *versionOutput) Write(data []byte) (int, error) {
	remaining := (4 << 10) - len(o.data)
	o.data = append(o.data, data[:min(len(data), remaining)]...)
	o.truncated = o.truncated || len(data) > remaining
	return len(data), nil
}
