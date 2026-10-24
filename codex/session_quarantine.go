package codex

import (
	"encoding/json"
	"time"
)

// UnattributedRecord is diagnostic material, excluded from Session live streams.
// Raw is not an answer capability and is never replayed after later association.
type UnattributedRecord struct {
	Sequence   uint64
	ReceivedAt time.Time
	Raw        json.RawMessage
}

// UnattributedSnapshot declares the diagnostic retention range and lost coverage.
// Evicted includes oversized records; Expired counts retention expiry.
type UnattributedSnapshot struct {
	Records                             []UnattributedRecord
	MaxRecords, MaxBytes, RetainedBytes int
	Retention                           time.Duration
	Received, Evicted, Expired          uint64
	CoverageGap                         bool
}

func (c *Client) expireQuarantineLocked(now time.Time) {
	for len(c.quarantine) > 0 && !now.Before(c.quarantine[0].ReceivedAt.Add(c.limits.QuarantineRetention)) {
		c.removeQuarantineLocked()
		c.quarantineExpired = saturatingAdd(c.quarantineExpired, 1)
	}
}
func (c *Client) removeQuarantineLocked() {
	c.quarantineBytes -= len(c.quarantine[0].Raw)
	c.quarantine[0] = UnattributedRecord{}
	c.quarantine = c.quarantine[1:]
	if len(c.quarantine) == 0 {
		c.quarantine = nil
	}
}
func (c *Client) scheduleQuarantineLocked() {
	if c.quarantineTimer != nil {
		c.quarantineTimer.Stop()
	}
	if len(c.quarantine) == 0 {
		return
	}
	c.quarantineTimer = time.AfterFunc(time.Until(c.quarantine[0].ReceivedAt.Add(c.limits.QuarantineRetention)), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.expireQuarantineLocked(time.Now())
		c.scheduleQuarantineLocked()
	})
}
func (c *Client) quarantineLocked(raw []byte) {
	now := time.Now()
	c.expireQuarantineLocked(now)
	c.quarantineSequence = saturatingAdd(c.quarantineSequence, 1)
	if len(raw) > c.limits.QuarantineBytes {
		c.quarantineEvicted = saturatingAdd(c.quarantineEvicted, 1)
		return
	}
	for len(c.quarantine) >= c.limits.QuarantineRecords || c.quarantineBytes+len(raw) > c.limits.QuarantineBytes {
		c.removeQuarantineLocked()
		c.quarantineEvicted = saturatingAdd(c.quarantineEvicted, 1)
	}
	c.quarantine = append(c.quarantine, UnattributedRecord{Sequence: c.quarantineSequence, ReceivedAt: now, Raw: append(json.RawMessage(nil), raw...)})
	c.quarantineBytes += len(raw)
	c.scheduleQuarantineLocked()
}

// Unattributed reads independent diagnostic copies, never a second event stream.
func (c *Client) Unattributed() UnattributedSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireQuarantineLocked(time.Now())
	result := UnattributedSnapshot{MaxRecords: c.limits.QuarantineRecords, MaxBytes: c.limits.QuarantineBytes, Retention: c.limits.QuarantineRetention, RetainedBytes: c.quarantineBytes, Received: c.quarantineSequence, Evicted: c.quarantineEvicted, Expired: c.quarantineExpired, CoverageGap: c.quarantineSequence > 0}
	for _, record := range c.quarantine {
		record.Raw = append(json.RawMessage(nil), record.Raw...)
		result.Records = append(result.Records, record)
	}
	return result
}
