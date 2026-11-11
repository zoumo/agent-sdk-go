package codex_test

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionShellEnvStartAndResume(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "resume"}[resume], func(t *testing.T) {
			mode, method := "normal", "thread/start"
			if resume {
				mode, method = "resume-normal", "thread/resume"
			}
			f := startFake(t, mode, codex.Limits{})
			raw := map[string]json.RawMessage{"shell_environment_policy.inherit": json.RawMessage(`"none"`)}
			before := maps.Clone(raw)
			env := map[string]string{"TOKEN": "literal=$HOME=附加", "EMPTY": ""}
			options := codex.SessionOptions{Client: f.c, ShellEnv: env}
			params := &types.ThreadResumeParams{ThreadID: resumedNativeID, Config: types.Some(raw)}
			if resume {
				options.Resume = params
			} else {
				options.Thread.Config = types.Some(raw)
			}
			s, err := codex.Open(context.Background(), options)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, s.Close()) })
			config := configFromFrame(t, requireFrame(t, f, method))
			assert.JSONEq(t, `{"TOKEN":"literal=$HOME=附加","EMPTY":""}`, string(config["shell_environment_policy.set"]))
			assert.Equal(t, before, raw)
			assert.Equal(t, map[string]string{"TOKEN": "literal=$HOME=附加", "EMPTY": ""}, env)
			env["TOKEN"] = "changed"
			assert.JSONEq(t, `{"TOKEN":"literal=$HOME=附加","EMPTY":""}`, string(config["shell_environment_policy.set"]))
		})
	}
}

func TestSessionShellEnvRejectsBeforeProcessStart(t *testing.T) {
	const secret = "secret-shell-value"
	for _, key := range []string{"CODEX_SESSION_ID", "CODEX_THREAD_ID", "CODEX_TOOL_CALL_ID", "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN", "NODE_REPL_AUTH_TOKEN", "CODEX_GUARDIAN_DECISIONS_API_KEY", "OPENAI_FEDERATION_RULE_ID", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_WORKLOAD_IDENTITY_CONTEXT"} {
		for _, name := range []string{key, strings.ToLower(key)} {
			t.Run(name, func(t *testing.T) {
				_, err := codex.Open(context.Background(), codex.SessionOptions{Stdio: &codex.StdioOptions{Executable: "/does-not-exist"}, ShellEnv: map[string]string{name: secret}})
				require.ErrorIs(t, err, codex.ErrInvalidInput)
				assert.NotContains(t, err.Error(), secret)
			})
		}
	}
	for _, path := range []string{"shell_environment_policy", "shell_environment_policy.set", "shell_environment_policy.set.TOKEN"} {
		for _, resume := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/start", true: "/resume"}[resume], func(t *testing.T) {
				raw := map[string]json.RawMessage{path: json.RawMessage(`"secret-config-value"`)}
				before := maps.Clone(raw)
				options := codex.SessionOptions{Stdio: &codex.StdioOptions{Executable: "/does-not-exist"}, ShellEnv: map[string]string{"TOKEN": secret}}
				if resume {
					options.Resume = &types.ThreadResumeParams{ThreadID: "root", Config: types.Some(raw)}
				} else {
					options.Thread.Config = types.Some(raw)
				}
				_, err := codex.Open(context.Background(), options)
				require.ErrorIs(t, err, codex.ErrInvalidInput)
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, err.Error(), "secret-config-value")
				assert.Equal(t, before, raw)
			})
		}
	}
}

func TestSessionShellEnvInvalidDeclarationsAreDeterministic(t *testing.T) {
	for _, env := range []map[string]string{
		{"": "secret-env-value"},
		{"A=B": "secret-env-value"},
		{"A\x00B": "secret-env-value"},
		{string([]byte{0xff}): "secret-env-value"},
		{"TOKEN": "secret-env-value\x00"},
		{"TOKEN": "secret-env-value" + string([]byte{0xff})},
		{"Path": "secret-env-value", "PATH": "other-secret"},
		{"Z": "secret-env-value\x00", "A": "secret-env-value\x00"},
	} {
		options := codex.SessionOptions{Stdio: &codex.StdioOptions{Executable: "/does-not-exist"}, ShellEnv: env}
		_, err := codex.Open(context.Background(), options)
		require.ErrorIs(t, err, codex.ErrInvalidInput)
		assert.NotContains(t, err.Error(), "secret-env-value")
		assert.NotContains(t, err.Error(), "other-secret")
		for range 10 {
			_, again := codex.Open(context.Background(), options)
			assert.EqualError(t, again, err.Error())
		}
	}
}
