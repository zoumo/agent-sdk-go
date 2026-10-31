package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Launchers adapt SDKs that accept only an executable path. The runner alone
// chooses the protocol command, inputs and receipt for each independent run.
func writePeerLauncher(root, phase string, peer command) (string, error) {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	words := []string{quote(peer.Command)}
	for _, arg := range peer.Args {
		words = append(words, quote(arg))
	}
	path := filepath.Join(root, "peer-"+phase)
	return path, os.WriteFile(path, []byte("#!/bin/sh\nexec "+strings.Join(words, " ")+"\n"), 0700)
}

func preparePolicyCopy(root, target string) error {
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	for _, name := range []string{"raw", "config", "plan.json", "report.json"} {
		if err := os.Symlink(filepath.Join(root, name), filepath.Join(target, name)); err != nil {
			return err
		}
	}
	return nil
}
