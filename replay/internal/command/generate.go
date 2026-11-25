//go:build builtin_generate

package command

import (
	"github.com/spf13/cobra"

	"github.com/zoumo/agent-sdk-go/replay/internal/builtin"
)

func registerGenerate(root *cobra.Command, streams IO) {
	o := builtin.GenerateOptions{Root: "replay"}
	root.Flags().BoolVar(&o.Check, "check", false, "Reject stale or missing generated artifacts")
	root.Flags().StringVar(&o.Revision, "revision", "", "Source repository revision (required when generating)")
	root.Flags().StringVar(&o.Root, "root", "replay", "Directory containing Replay data")
	root.RunE = func(_ *cobra.Command, _ []string) error {
		o.Diagnostics = streams.Err
		return builtin.Generate(o)
	}
}
