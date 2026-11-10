package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "use: mutate-request REQUEST")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	peer, _ := request["peer"].(map[string]any)
	remote, _ := peer["remote"].(map[string]any)
	connections, _ := remote["connections"].(map[string]any)
	probe, _ := connections["probe"].(map[string]any)
	if probe == nil {
		fmt.Fprintln(os.Stderr, "request has no probe endpoint")
		os.Exit(2)
	}
	probe["url"] = ""
	data, err = json.Marshal(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
