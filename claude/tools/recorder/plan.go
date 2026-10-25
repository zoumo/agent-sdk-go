// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"fmt"
	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

const CLIVersion = transcript.RecordingVersion

// Scene describes only reviewed root operations. Values are copied per lookup;
// changing a returned plan cannot widen the durable ledger's budgets.
type Scene struct {
	Name    string
	Prompts []string
	Turns   int
	Persist bool
}

// Plan never grants live authority. R1 uses separate isolated persistence for R4;
// R4 itself uses the existing local query APIs and never opens a CLI or resumes.
func Plan(name string) (Scene, error) {
	switch name {
	case "s1":
		return Scene{Name: name, Prompts: []string{s1}, Turns: 1}, nil
	case "s2":
		return Scene{Name: name, Prompts: []string{s2}, Turns: 1}, nil
	case "r1":
		return Scene{Name: name, Prompts: []string{"Reply with the word Ready.", "Reply with the word Done."}, Turns: 2, Persist: true}, nil
	case "r2":
		return Scene{Name: name, Prompts: []string{"Read numbers.txt, then write summary.txt with total=18.", "Ask two questions once: choose Blue or Green; enter a short label."}, Turns: 2}, nil
	case "r3":
		return Scene{Name: name, Prompts: []string{"Produce a short numbered list from one to ten.", "Also include the word sample."}, Turns: 2}, nil
	default:
		return Scene{}, fmt.Errorf("unsupported root scene (children, retry and resume disabled)")
	}
}
