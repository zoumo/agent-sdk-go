package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	pisim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/pi/go"
)

func runPiExtensionFlow(args []string) (err error) {
	if len(args) != 1 {
		return errors.New("use --pi-extension-flow /absolute/request.json")
	}
	var request struct {
		pisim.ExtensionFlow
		Output string
	}
	if err := readPiExtensionJSON(args[0], &request); err != nil {
		return err
	}
	if !filepath.IsAbs(request.Output) {
		return errors.New("pi: extension flow requires an absolute output path")
	}
	output, err := os.OpenFile(request.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return pisim.RunExtensionFlow(ctx, request.ExtensionFlow, output, nil)
}

func readPiExtensionJSON(path string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}
