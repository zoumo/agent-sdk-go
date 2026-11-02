package mocktest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Capture authors a manifest-bound EXPLICITLY SYNTHETIC external input from an
// SDK fixture. It is not an adopted recording and contains no matching engine.
func Capture(dir, provider string, data []byte, options Options) (string, error) {
	var envelope struct {
		Version   int
		TimeoutMS int `json:"timeoutMs"`
		Frames    []json.RawMessage
	}
	rows := []json.RawMessage{}
	timeout := 0
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) && json.Unmarshal(data, &envelope) == nil && envelope.Frames != nil {
		if envelope.Version != 1 {
			return "", fmt.Errorf("unsupported synthetic version")
		}
		rows = envelope.Frames
		timeout = envelope.TimeoutMS
	} else {
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				rows = append(rows, json.RawMessage(line))
			}
		}
	}
	if options.MaxFrames > 0 && len(rows) > options.MaxFrames {
		return "", fmt.Errorf("frame count exceeds limit")
	}
	if options.Handshakes != "" && (provider != "codex" || (options.Handshakes != "answer" && options.Handshakes != "answer-and-assert")) {
		return "", fmt.Errorf("invalid fixture handshakes")
	}
	var expanded []json.RawMessage
	barrier := func(method string) {
		id := fmt.Sprintf("sdk-fixture-barrier-%d", len(expanded))
		for _, entry := range []struct{ direction, wire string }{{"c2s", fmt.Sprintf(`{"id":%q,"method":%q}`, id, method)}, {"s2c", fmt.Sprintf(`{"id":%q,"result":{}}`, id)}} {
			b, _ := json.Marshal(map[string]any{"direction": entry.direction, "channel": "protocol", "epoch": 1, "wire": json.RawMessage(entry.wire)})
			expanded = append(expanded, b)
		}
	}
	for _, row := range rows {
		var f struct {
			Direction string
			Wire      map[string]json.RawMessage
		}
		if err := json.Unmarshal(row, &f); err != nil {
			return "", err
		}
		if options.RecordingVersion != "" {
			if version, ok := f.Wire["claude_code_version"]; ok && string(version) != fmt.Sprintf("%q", options.RecordingVersion) {
				return "", fmt.Errorf("native source version mismatch")
			}
		}
		expanded = append(expanded, row)
		if options.Handshakes != "" && f.Direction == "c2s" && f.Wire["id"] != nil && f.Wire["method"] == nil {
			barrier("replay/answer")
		}
	}
	if options.Handshakes == "answer-and-assert" {
		barrier("replay/assert")
	}
	// Only synthetic envelope ordinals are assigned; native wire RawMessages and
	// numeric lexemes remain unchanged. Original source fixtures are never edited.
	var raw bytes.Buffer
	for i, row := range expanded {
		var f map[string]json.RawMessage
		if err := json.Unmarshal(row, &f); err != nil {
			return "", err
		}
		f["ordinal"] = json.RawMessage(fmt.Sprint(i + 1))
		raw.WriteByte('{')
		keys := make([]string, 0, len(f))
		for k := range f {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for j, k := range keys {
			if j > 0 {
				raw.WriteByte(',')
			}
			key, _ := json.Marshal(k)
			raw.Write(key)
			raw.WriteByte(':')
			raw.Write(f[k])
		}
		raw.WriteByte('}')
		raw.WriteByte('\n')
	}
	root, err := os.MkdirTemp(dir, "synthetic-capture-")
	if err != nil {
		return "", err
	}
	if err = os.Mkdir(filepath.Join(root, "raw"), 0700); err != nil {
		return "", err
	}
	plan, _ := json.Marshal(map[string]any{"provider": provider, "provenance": "synthetic"})
	// A JSONL stream alone has no provenance header. Keep fault injection explicitly
	// synthetic in the envelope rather than relying on manifest self-description.
	var finalRows []json.RawMessage
	for _, line := range bytes.Split(bytes.TrimSpace(raw.Bytes()), []byte("\n")) {
		finalRows = append(finalRows, json.RawMessage(line))
	}
	var frozen bytes.Buffer
	fmt.Fprintf(&frozen, `{"version":1,"provenance":"synthetic","timeoutMs":%d,"frames":[`, timeout)
	for i, row := range finalRows {
		if i > 0 {
			frozen.WriteByte(',')
		}
		frozen.Write(row)
	}
	frozen.WriteString("]}")
	b := frozen.Bytes()
	manifest, _ := json.Marshal(map[string]any{"provenance": "synthetic", "planSHA256": hash(plan), "files": map[string]string{"raw.jsonl": hash(b)}})
	for name, content := range map[string][]byte{"plan.json": plan, "manifest.json": manifest, "raw/raw.jsonl": b} {
		if err = os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			return "", err
		}
	}
	return root, nil
}

// Arguments exposes the new mock interface to SDK tests with an argument API.
func Arguments(dir, provider, scenario, receipt string) ([]string, error) {
	data, err := os.ReadFile(scenario)
	if err != nil {
		return nil, err
	}
	root, err := Capture(dir, provider, data, Options{})
	if err != nil {
		return nil, err
	}
	if receipt == "" {
		receipt = filepath.Join(root, "..", filepath.Base(root)+".receipt.json")
	}
	return []string{"mock", "--provider", provider, "--recording", root, "--report", receipt}, nil
}
