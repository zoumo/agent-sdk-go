package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Only echoes authored synthetic frames and records local protocol input.
func claudeChildDetailPeer() error {
	trace, err := os.Create(os.Getenv("SDK_INTEGRATION_TRACE"))
	if err != nil {
		return err
	}
	defer trace.Close()
	args, _ := json.Marshal(os.Args[1:])
	fmt.Fprintln(trace, string(args))
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		fmt.Fprintln(trace, string(raw))
		var f struct {
			Type, RequestID string
			Request         struct{ Subtype string }
			Message         struct{ Content string }
			Response        json.RawMessage
		}
		// request_id uses its native spelling, unlike the local fixture instruction.
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		_ = json.Unmarshal(wire["request_id"], &f.RequestID)
		switch f.Type {
		case "control_request":
			fmt.Fprintf(os.Stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`+"\n", f.RequestID)
		case "user":
			fmt.Fprintln(os.Stdout, f.Message.Content)
		}
	}
	return scanner.Err()
}
