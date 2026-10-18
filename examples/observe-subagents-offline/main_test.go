package main

import (
	"bytes"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--fixture" {
		if fixture() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Verify the documented transcript through the public client and fake process.
func TestOfflineTranscript(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("expected.txt")
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != string(expected) {
		t.Fatalf("transcript differs:\n%s", output.String())
	}
}
