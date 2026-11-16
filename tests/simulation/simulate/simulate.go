// SDK-owned offline simulation. The external tool owns protocol/material work.
package simulate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, 10), 0o600)
}

func readJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}

type options struct {
	Recording, Provider, Case, Output, Driver string
	Jobs                                      int
	Timeout                                   time.Duration
	Gate                                      bool
}

func execute(ctx context.Context, o options) error {
	if !filepath.IsAbs(o.Output) || !filepath.IsAbs(o.Driver) || o.Jobs < 1 || o.Timeout <= 0 {
		return errors.New("absolute --output/--driver, positive --jobs/--timeout required")
	}
	if err := os.Mkdir(o.Output, 0o700); err != nil {
		return err
	}
	tool, err := discoverTool()
	if err != nil {
		return err
	}
	if o.Gate {
		return gate(ctx, tool, o)
	}
	source := sourceSelection{Provider: o.Provider, Case: o.Case}
	if o.Recording != "" {
		if !filepath.IsAbs(o.Recording) {
			return errors.New("--recording must be absolute")
		}
		source = sourceSelection{Recording: o.Recording}
	}
	return simulate(ctx, tool, o, source)
}
