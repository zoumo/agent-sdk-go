package tracecontext

import (
	"errors"
	"strings"
)

// Validate checks the W3C carrier without changing it. Version 00 has exactly
// 55 bytes; future versions other than ff may append a dash-delimited suffix.
// IDs and flags use lowercase hex and both IDs must be nonzero. Tracestate
// accepts W3C list members, optional surrounding whitespace and unique keys.
func Validate(parent, state string) error {
	if len(parent) < 55 || !traceASCII(parent, false) ||
		parent[2] != '-' || parent[35] != '-' || parent[52] != '-' ||
		!traceHex(parent[:2]) || parent[:2] == "ff" ||
		!traceHex(parent[3:35]) || parent[3:35] == strings.Repeat("0", 32) ||
		!traceHex(parent[36:52]) || parent[36:52] == strings.Repeat("0", 16) ||
		!traceHex(parent[53:55]) {
		return errors.New("invalid W3C traceparent")
	}
	if len(parent) != 55 && (parent[:2] == "00" || parent[55] != '-') {
		return errors.New("invalid W3C traceparent version length or suffix")
	}
	if !validTracestate(state) {
		return errors.New("invalid W3C tracestate")
	}
	return nil
}

func traceASCII(value string, whitespace bool) bool {
	for i := range len(value) {
		if whitespace && value[i] == '\t' {
			continue
		}
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func traceHex(value string) bool {
	for i := range len(value) {
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return false
		}
	}
	return true
}

func validTracestate(state string) bool {
	if state == "" {
		return true
	}
	if !traceASCII(state, true) {
		return false
	}
	members := strings.Split(state, ",")
	if len(members) > 32 {
		return false
	}
	keys := make(map[string]bool, len(members))
	for _, member := range members {
		member = strings.Trim(member, " \t")
		// W3C permits empty list members when multiple headers are combined.
		if member == "" {
			continue
		}
		key, value, ok := strings.Cut(member, "=")
		if !ok || !traceKey(key) || keys[key] || len(value) == 0 || len(value) > 256 {
			return false
		}
		for i := range len(value) {
			if value[i] < 0x20 || value[i] > 0x7e || value[i] == '=' {
				return false
			}
		}
		keys[key] = true
	}
	return true
}

func traceKey(key string) bool {
	if tenant, system, multi := strings.Cut(key, "@"); multi {
		return traceKeyPart(tenant, 241, true) && traceKeyPart(system, 14, false)
	}
	return traceKeyPart(key, 256, false)
}

func traceKeyPart(part string, maxBytes int, digitStart bool) bool {
	if len(part) == 0 || len(part) > maxBytes {
		return false
	}
	alpha := func(c byte) bool { return c >= 'a' && c <= 'z' }
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	if !alpha(part[0]) && (!digitStart || !digit(part[0])) {
		return false
	}
	for i := range len(part) {
		c := part[i]
		if !alpha(c) && !digit(c) && c != '_' && c != '-' && c != '*' && c != '/' {
			return false
		}
	}
	return true
}
