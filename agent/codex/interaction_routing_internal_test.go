package agentcodex

import "testing"

func TestConfirmedInteractionThreadRequiresSessionTree(t *testing.T) {
	parents := map[string]string{
		"child":       "root",
		"grandchild":  "child",
		"foreign":     "foreign-root",
		"cyclic-a":    "cyclic-b",
		"cyclic-b":    "cyclic-a",
		"unconfirmed": "",
	}
	parentThread := func(threadID string) string {
		return parents[threadID]
	}

	tests := []struct {
		name     string
		threadID string
		want     string
	}{
		{name: "root", threadID: "root", want: "root"},
		{name: "direct child", threadID: "child", want: "child"},
		{name: "nested child", threadID: "grandchild", want: "grandchild"},
		{name: "foreign tree", threadID: "foreign", want: ""},
		{name: "unconfirmed parent", threadID: "unconfirmed", want: ""},
		{name: "cycle", threadID: "cyclic-a", want: ""},
		{name: "empty identity", threadID: "", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := confirmedInteractionThread("root", test.threadID, parentThread)
			if got != test.want {
				t.Fatalf("confirmedInteractionThread() = %q, want %q", got, test.want)
			}
		})
	}
}
