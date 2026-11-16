package simulate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func withSimulationOutput(output, archiveRoot string, run func(string) error) (artifacts string, err error) {
	if output != "" {
		return output, run(output)
	}
	root, err := os.MkdirTemp("/tmp", "sdk-simulation.")
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root, errors.Join(err, os.RemoveAll(root))
	}
	output = filepath.Join(physical, "gate")
	defer func() {
		archive, archiveErr := archiveSimulation(output, archiveRoot)
		if archiveErr != nil {
			artifacts = output
			err = errors.Join(err, fmt.Errorf("archive failed; simulation output retained at %s: %w", output, archiveErr))
			return
		}
		artifacts = archive
		err = errors.Join(err, os.RemoveAll(physical))
	}()
	return output, run(output)
}

func archiveSimulation(output, archiveRoot string) (string, error) {
	archiveRoot, err := filepath.Abs(archiveRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
		return "", err
	}
	archive, err := os.MkdirTemp(archiveRoot, "gate.")
	if err != nil {
		return "", err
	}
	if err := os.CopyFS(archive, os.DirFS(output)); err != nil {
		return archive, fmt.Errorf("partial archive at %s: %w", archive, err)
	}
	return archive, nil
}
