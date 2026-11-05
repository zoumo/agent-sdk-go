package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type command struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"-"`
	Dir     string   `json:"-"`
}

type toolIdentity struct {
	Command string
	Version string
	SHA256  string
}

func discoverTool(ctx context.Context) (toolIdentity, error) {
	path := os.Getenv("AGENT_SDK_TESTIFY_BIN")
	if path == "" {
		return toolIdentity{}, errors.New("no published testify pin; set absolute AGENT_SDK_TESTIFY_BIN for local integration")
	}
	if !filepath.IsAbs(path) {
		return toolIdentity{}, errors.New("AGENT_SDK_TESTIFY_BIN must be absolute")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return toolIdentity{}, err
	}
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return toolIdentity{}, err
	}
	return toolIdentity{path, strings.TrimSpace(string(out)), digest(data)}, nil
}
