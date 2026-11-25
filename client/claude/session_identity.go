package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// SessionIdentityResult projects only identity fields from a correlated native
// get_status response. Err reports a query failure or an invalid response.
type SessionIdentityResult struct {
	SessionID string
	Cwd       string
	Err       error
}

// ProbeIdentity queries get_status without input or model work and appends one
// result to Receive, after records already admitted by the native reader.
// A successful result fixes the native observation identity for later frames;
// it must agree with an identity already observed or explicitly requested.
// Account and other status rows are discarded. The return error concerns result
// delivery; the native query outcome is carried by SessionIdentityResult.Err.
func (s *Session) ProbeIdentity(ctx context.Context) error {
	if ctx == nil {
		return queue.ErrNilContext
	}
	raw, err := s.client.control(ctx, "get_status", map[string]any{"subtype": "get_status"})
	var result SessionIdentityResult
	if err != nil {
		result.Err = errors.New("native get_status query failed")
		if errors.Is(err, ErrProtocol) {
			result.Err = fmt.Errorf("%w: invalid get_status response", ErrProtocol)
		} else if ctx.Err() != nil {
			result.Err = ctx.Err()
		}
	} else {
		result = parseStatusIdentity(raw)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if result.Err != nil {
			return result.Err
		}
		return ErrClosed
	}
	if result.Err == nil {
		known := s.admission.sessionID()
		if (known != "" && known != result.SessionID) ||
			(s.requestedID != "" && s.requestedID != result.SessionID) ||
			!sameClaimDirectory(result.Cwd, s.admission.cwd) {
			result.Err = fmt.Errorf("%w: get_status conflicts with the native session identity", ErrProtocol)
		} else if known == "" {
			for _, observation := range s.admission.claimRoot(result.SessionID) {
				if err := s.fifo.Push(observation, 0); err != nil {
					return err
				}
			}
		} else {
			s.admission.assigned = true
		}
	}
	return s.fifo.Push(Received{Kind: ReceivedIdentity, Identity: &result}, int64(len(result.SessionID)+len(result.Cwd)))
}

func parseStatusIdentity(raw json.RawMessage) SessionIdentityResult {
	invalid := SessionIdentityResult{Err: fmt.Errorf("%w: invalid get_status identity response", ErrProtocol)}
	if !utf8.Valid(raw) || validateUniqueJSONMembers(raw) != nil {
		return invalid
	}
	var status struct {
		Sections []struct {
			Rows []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"rows"`
		} `json:"sections"`
	}
	if json.Unmarshal(raw, &status) != nil {
		return invalid
	}
	var result SessionIdentityResult
	ids, cwds := 0, 0
	for _, section := range status.Sections {
		for _, row := range section.Rows {
			switch row.Label {
			case "Session ID":
				ids++
				result.SessionID = row.Value
			case "cwd":
				cwds++
				result.Cwd = row.Value
			}
		}
	}
	if ids != 1 || cwds != 1 || result.SessionID == "" || !filepath.IsAbs(result.Cwd) {
		return invalid
	}
	return result
}
