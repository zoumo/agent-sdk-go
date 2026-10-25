---
type: Architecture Decision
title: Disconnect reporting
status: stable
---

# Disconnect reporting

SDKs report connection loss without automatic reconnect or missed-event recovery. Applications decide whether to open another connection and invoke native resume explicitly. Transport loss does not establish native execution termination.

Native resume and retained-history queries have separate lifecycles. A query does not restore interaction or approval authority. Same-object recovery, subscription restoration and approval-handle recovery remain outside the current contract.

Independent metadata/content reads can run without a Session or alongside an
existing one; they neither fill missed Receive records nor restore membership,
subscriptions or original answer handles. Saved content and retained raw source
are distinct from a complete event log. Native resume remains an explicit action.
See the [history-query contract](../contracts/history-query.md).
