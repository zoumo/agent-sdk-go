# Pi session extension

This directory contains the unmodified Apache-2.0 session extension from
agent-sdk-go revision `2dfc08f88c74d5ed3e8390ffabf710e0a55ff28d`. The source path
is `agent/pi/extensions/session.ts`.

SHA-256 `298c12cce0fe179295cfb1c01d4ae9fa9283c1525c593c6b4dd8be82ea57683b`.

The SDK embeds this file from `agent/pi/extensions/session.ts`. Driver packaging stages the same bytes at `pi-extension/session.ts`.

Replay stages these bytes in its private recording run with a session
configuration generated from the selected case. The package license and
[NOTICE](NOTICE) provide attribution.
