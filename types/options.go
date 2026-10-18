// SPDX-License-Identifier: Apache-2.0

package types

import "time"

// Limits bounds transport memory. Zero fields select defaults. A bound exceeded
// by incoming traffic terminates the connection explicitly; frames are not
// silently discarded and the stdout reader never waits for an event consumer.
// Queue/count limits must be at most 65,536, except PermissionWorkers (1,024)
// and PermissionHistory (1,048,576). FrameBytes is at most 64 MiB and StderrBytes
// at most 16 MiB. Invalid configuration is rejected before starting a process.
type Limits struct {
	EventBuffer     int
	FrameBytes      int
	PendingControls int
	ControlBuffer   int
	StderrBytes     int
	// PendingPermissions bounds unanswered incoming controls, including retained
	// permission handles.
	PendingPermissions int
	// PermissionWorkers bounds concurrent handler calls, including blocked ones.
	PermissionWorkers int
	// PermissionHistory bounds distinct incoming control/cancellation IDs per client.
	// IDs are never evicted, so a retired request cannot acquire a new responder.
	PermissionHistory int
}

// Options is the callback-free configuration for a local CLI process.
// Use claude.OptionsFromTypes to adapt it to runtime options, then optionally
// attach a root-package PermissionHandler. Env slices retain normal Go sharing.
// Zero values select the client defaults; validation occurs at Open.
type Options struct {
	CLIPath string
	WorkDir string
	Model   string
	// ResumeID asks a fresh CLI process to resume its native session. It is
	// passed literally; the SDK does not replay messages or restore pending work.
	ResumeID string
	// ForwardSubagentText requests native child text forwarding at initialize.
	// False omits the option; acknowledgment does not prove CLI support.
	ForwardSubagentText bool
	Env                 []string
	InitializeTimeout   time.Duration
	ShutdownTimeout     time.Duration
	Limits              Limits
}
