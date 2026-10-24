---
type: Specification
title: 事件交付合同
status: stable
---

# Event delivery

Both SDKs retain accepted events in dynamically growing FIFO storage. A paused
Codex `Subscription.Events()` reader or Claude `Receive` caller does not impose a
fixed event-count ceiling, block protocol reading, or prevent RPC responses and
permission requests from being routed. Each Codex subscription and each Claude
client owns exactly one ChannX instance.
The SDKs share the implementation, not an instance; there is no separate queue
per agent or native event category.
No preview events are dropped to make room for full events.

The private generic `internal/queue` wrapper directly uses
`github.com/zoumo/golib/channx.ChannX`, pinned by the root Go module. It adds
typed envelopes, cancellation-aware waiting, batch admission and lifecycle
handling. One wrapper goroutine owns all sends and Close operations, and ChannX
owns its buffer pump. Their unbuffered command/acknowledgment handshake is a
control rendezvous, not an additional event backlog. Codex also has its typed
channel delivery bridge. The wrapper has no protocol knowledge. Native
protocol parsing, relationship attribution, deduplication and permission authority
remain in their SDKs. Native `types` packages still depend only on the standard
library.

## Order and admission

A protocol reader preserves its native arrival order at admission. Codex also has
unresolved-event timer and lifecycle callbacks; their requests are serialized
before the stream owner writes to ChannX. Claude publishes from its sole stdout
reader. Each stream has one ChannX input owner, and there is no invented global
order across separate streams. A batch is accepted as one envelope, contiguously
and all or nothing, including
validation of caller-supplied sizes before storage changes. Codex batches native
events and their derived records into the same admission decision. Claude keeps
its existing bounded transaction scratch for one derived fanout, consumed before
reading the next native event; that scratch is not a second growing FIFO.
This does not change existing relationship attribution:
unresolved events may wait for relationship evidence, and released records follow
the native observer's existing ordering rules.

Canceling a queue wait or Claude `Receive(ctx)` cancels only that wait. A canceled
wait does not delete an accepted event or stop the session. Cancellation racing a
successful receive may still return the event already removed. Canceling the
context passed to Codex `Subscribe` ends that observation; it is not a receive
wait and does not interrupt provider work.

## Closing and draining

Codex `Subscription.Close()` ends local observation and rejects new admission,
while accepted events drain through `Events()`. The channel closes after draining.
`Done()` and `Err()` report observation termination independently of that drain.
Connection failure likewise preserves accepted events and reports its cause.
Use `Subscription.Abort()` when abandoning the channel: it discards unread
records, stops the delivery bridge and releases retained references. A receive
racing `Abort` may still obtain the bridge's in-flight event. Neither method
sends a provider interrupt. The bridge can hold one event outside the FIFO while
waiting for a channel receiver; drain it or abort it when finished.

Claude `Close()` stops the owned process and waits for process/reader cleanup,
without waiting for the caller to consume events. `Done()` closes on shutdown or
terminal failure. Accepted records remain available through `Receive`, followed
by the terminal error or `io.EOF` after successful shutdown. `Err()` can be read
before draining; cleanup may add exit diagnostics. Root turn completion and idle
are native events and do not close observation. Use `Client.Abort()` when
abandoning Claude consumption: it closes the client and discards the native
backlog; future `Receive` calls reject the abandoned stream. It does not mutate
the bounded relationship scratch concurrently with an active `Receive`.

Internally, closing a queue rejects pushes and wakes waiters, preserving the first
terminal cause while allowing drain. Aborting clears pending storage, preserves
an already established cause and wakes waiters. Abort actively drains and
discards upstream envelopes and joins the input owner; it costs O(backlog) and
does not need an external consumer. Close returns without joining the buffer
pump, which exits after drain. Callers must drain after Close or explicitly Abort
when abandoning a stream, so a blocked pump does not retain its backlog.
Zero-value queues work without initialization; nil queue receivers and nil wait contexts return defined errors.

## Memory, limits and diagnostics

The event backlog has no configured count or byte ceiling. Available machine
memory is the bound: sustained input faster than consumption can exhaust memory.
This is in-memory delivery, with no crash, OOM or process-restart durability
promise. Consume continuously where possible, stop unneeded observations and use
`Abort` for abandoned streams.

ChannX manages the dynamically growing ring; the wrapper does not copy its ring
implementation or maintain a second metadata FIFO. Consumed envelope slots are
cleared, and abort clears discarded envelopes. Buffer storage can shrink when
empty. This releases queued event references; it does not measure or bound all
memory reachable from a native event.

Codex `Limits.EventQueue` and `SubscriptionOptions.EventQueue`, and Claude
`Limits.EventBuffer`, are deprecated and ignored for event storage. Their
existing validation remains; their values neither allocate buffers nor limit
backlog. Frame/protocol bounds, subscription and graph limits, unresolved-event
count/byte/age limits, per-notification fanout bounds, deduplication storage,
control and permission limits remain separate and continue to fail visibly.
Changing event delivery does not change call timeouts or approval admission rules.

The shared queue's internal snapshot reports pending item count, caller-estimated
bytes and closed state. `OldestAge` is unavailable and always zero because
ChannX does not expose the oldest envelope. Byte estimates come from callers;
they are not heap measurements. Snapshots exclude values already popped into a
native staging/delivery step, including the Codex bridge's in-flight event.
These implementation diagnostics do not add a public SDK metrics API. Existing
native diagnostics continue to describe native routing and process failures.
