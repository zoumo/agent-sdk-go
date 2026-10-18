// Copyright 2026 Claude Agent SDK for Go contributors
// SPDX-License-Identifier: Apache-2.0

// Package types provides the SDK's pure event, content, permission-answer, and
// callback-free configuration data, plus the existing JSON decoding codecs.
// It can be imported without the root client package and owns no processes,
// connections, locks, queues, permission registries, or callback execution.
// SDK-derived relationship/observation envelopes are separate root-package types;
// native Event/ResultEvent.Origin retain the official MessageOrigin object.
// Unknown protocol data remains available as raw JSON. These handwritten
// views target the pinned official Python source, not full SDK parity or
// verified live CLI compatibility.
package types
