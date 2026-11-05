package mocktest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

type syntheticSource struct {
	plan struct {
		Provider   string `json:"provider"`
		CaseID     string `json:"caseId"`
		Provenance string `json:"provenance"`
	}
	planSHA256     string
	manifestSHA256 string
	rawSHA256      string
}

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
			return "", errors.New("unsupported synthetic version")
		}
		rows = envelope.Frames
		timeout = envelope.TimeoutMS
	} else {
		for line := range bytes.SplitSeq(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				rows = append(rows, json.RawMessage(line))
			}
		}
	}
	if options.MaxFrames > 0 && len(rows) > options.MaxFrames {
		return "", errors.New("frame count exceeds limit")
	}
	if options.Handshakes != "" && (provider != "codex" || (options.Handshakes != "answer" && options.Handshakes != "answer-and-assert")) {
		return "", errors.New("invalid fixture handshakes")
	}
	var expanded []json.RawMessage
	barrier := func(method string) error {
		id := fmt.Sprintf("sdk-fixture-barrier-%d", len(expanded))
		for _, entry := range []struct{ direction, wire string }{{"c2s", fmt.Sprintf(`{"id":%q,"method":%q}`, id, method)}, {"s2c", fmt.Sprintf(`{"id":%q,"result":{}}`, id)}} {
			b, err := json.Marshal(map[string]any{"direction": entry.direction, "channel": "protocol", "epoch": 1, "wire": json.RawMessage(entry.wire)})
			if err != nil {
				return err
			}
			expanded = append(expanded, b)
		}
		return nil
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
				return "", errors.New("native source version mismatch")
			}
		}
		expanded = append(expanded, row)
		if options.Handshakes != "" && f.Direction == "c2s" && f.Wire["id"] != nil && f.Wire["method"] == nil {
			if err := barrier("replay/answer"); err != nil {
				return "", err
			}
		}
	}
	if options.Handshakes == "answer-and-assert" {
		if err := barrier("replay/assert"); err != nil {
			return "", err
		}
	}
	// Only synthetic envelope ordinals are assigned; native wire RawMessages and
	// numeric lexemes remain unchanged. Original source fixtures are never edited.
	var raw bytes.Buffer
	for i, row := range expanded {
		var f map[string]json.RawMessage
		if err := json.Unmarshal(row, &f); err != nil {
			return "", err
		}
		f["ordinal"] = json.RawMessage(strconv.Itoa(i + 1))
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
			key, err := json.Marshal(k)
			if err != nil {
				return "", err
			}
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
	if err := os.Mkdir(filepath.Join(root, "raw"), 0o700); err != nil {
		return "", err
	}
	plan, err := json.Marshal(map[string]any{"provider": provider, "caseId": "sdk-synthetic-" + provider, "provenance": "synthetic"})
	if err != nil {
		return "", err
	}
	// A JSONL stream alone has no provenance header. Keep fault injection explicitly
	// synthetic in the envelope rather than relying on manifest self-description.
	var finalRows []json.RawMessage
	for line := range bytes.SplitSeq(bytes.TrimSpace(raw.Bytes()), []byte("\n")) {
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
	manifest, err := json.Marshal(map[string]any{"provenance": "synthetic", "planSHA256": hash(plan), "files": map[string]string{"raw.jsonl": hash(b)}})
	if err != nil {
		return "", err
	}
	for name, content := range map[string][]byte{"plan.json": plan, "manifest.json": manifest, "raw/raw.jsonl": b} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			return "", err
		}
	}
	return root, nil
}

func inspectSyntheticSource(root, provider string) (*syntheticSource, error) {
	source := &syntheticSource{}
	plan, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(plan, &source.plan); err != nil {
		return nil, fmt.Errorf("synthetic plan: %w", err)
	}
	if source.plan.Provider != provider || source.plan.CaseID == "" || source.plan.Provenance != "synthetic" {
		return nil, errors.New("synthetic plan identity is incomplete")
	}
	manifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var metadata struct {
		Provenance string            `json:"provenance"`
		PlanSHA256 string            `json:"planSHA256"`
		Files      map[string]string `json:"files"`
	}
	if err := json.Unmarshal(manifest, &metadata); err != nil {
		return nil, fmt.Errorf("synthetic manifest: %w", err)
	}
	source.planSHA256 = hash(plan)
	source.manifestSHA256 = hash(manifest)
	source.rawSHA256 = metadata.Files["raw.jsonl"]
	if metadata.Provenance != "synthetic" || metadata.PlanSHA256 != source.planSHA256 || source.rawSHA256 == "" {
		return nil, errors.New("synthetic manifest hashes or provenance are invalid")
	}
	raw, err := os.ReadFile(filepath.Join(root, "raw", "raw.jsonl"))
	if err != nil {
		return nil, err
	}
	if hash(raw) != source.rawSHA256 {
		return nil, errors.New("synthetic raw stream hash mismatch")
	}
	return source, nil
}
