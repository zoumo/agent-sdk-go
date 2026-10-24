package codex_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const treeChildParams = `{ "threadId" : "child-1", "turnId":"child-turn", "itemId":"child-item", "delta":"child text", "future":[null,9007199254740993] }`
const treeChildRaw = `{"method":"item/agentMessage/delta","params":` + treeChildParams + `,"futureEnvelope":null}`

func treeSpawnParams(tool, status string) string {
	return `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":8,"item":{"type":"collabAgentToolCall","id":"spawn-1","agentsStates":{"child-1":{"status":"running","message":null}},"receiverThreadIds":["child-1","child-1","child-2"],"senderThreadId":"native-thread-42","status":"` + status + `","tool":"` + tool + `","prompt":null,"model":null,"reasoningEffort":null,"future":null}}`
}

const boundsRelation = `{"thread":{"id":"child-1","parentThreadId":"native-thread-42"}}`
const childQuestion = `{"threadId":"child-1","turnId":"child-turn","itemId":"question-item","isBlocking":false,"questions":[{"id":"q","header":"H","question":"Choose","isOther":false,"isSecret":false,"options":[]}],"autoResolutionMs":null,"future":null}`

func handleTreePeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if handleLifecycleTreePeer(mode, m, send) || handleNestedTreePeer(mode, m, send) {
		return true
	}
	if !strings.HasPrefix(mode, "tree-") {
		return false
	}
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	switch m.Method {
	case "turn/start":
		emit("turn/started", `{"threadId":"native-thread-42","turn":{"id":"root-turn","items":[],"status":"inProgress"}}`)
		emit("future/other", `{"threadId":"unrelated-root","secret":null}`)
		emit("thread/started", `{"thread":{"id":"unrelated-root","future":null}}`)
		tool, status := "spawnAgent", "completed"
		if mode == "tree-failed" {
			status = "failed"
		}
		if mode == "tree-resume" {
			tool = "resumeAgent"
		}
		emit("item/completed", treeSpawnParams(tool, status))
		// Repeated native relations are distinct received facts.
		emit("item/completed", treeSpawnParams(tool, status))
		fmt.Fprintln(os.Stdout, treeChildRaw)
		fmt.Fprintln(os.Stdout, treeChildRaw)
		emit("future/child", `{"threadId":"child-2","opaque":null}`)
		emit("item/agentMessage/delta", `{"threadId":"child-1","turnId":"child-turn","itemId":"broken"}`)
		emit("future/null", `null`)
		fmt.Fprintln(os.Stdout, `{"method":"future/absent"}`)
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "tree-emit":
		fmt.Fprintln(os.Stdout, treeChildRaw)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}

func handleNestedTreePeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if mode != "tree-nested" && mode != "tree-pending" {
		return false
	}
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	relation := func(child, parent string) {
		emit("thread/started", `{"thread":{"id":"`+child+`","parentThreadId":"`+parent+`","future":null}}`)
	}
	spawn := func(parent, child, turn string) {
		p := strings.ReplaceAll(treeSpawnParams("spawnAgent", "completed"), "native-thread-42", parent)
		p = strings.ReplaceAll(p, `"child-1","child-1","child-2"`, `"`+child+`"`)
		p = strings.ReplaceAll(p, "root-turn", turn)
		emit("item/completed", p)
	}
	switch m.Method {
	case "turn/start":
		if mode == "tree-pending" {
			fmt.Fprintln(os.Stdout, treeChildRaw)
			emit("future/late", `{"threadId":"late","opaque":null}`)
		} else {
			// Grandchild events, then its parent relation, arrive before the root link.
			emit("future/grand", `{"threadId":"grand","ordinal":1,"future":null}`)
			emit("future/grand", `{"threadId":"grand","ordinal":2}`)
			relation("grand", "child-1")
			fmt.Fprintln(os.Stdout, treeChildRaw)
			// Native source gives the same parent independently of collab evidence.
			emit("thread/started", `{"thread":{"id":"child-1","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"native-thread-42","depth":1}}}}}`)
			emit("item/started", treeSpawnParams("spawnAgent", "completed"))
			spawn("native-thread-42", "child-1", "root-turn")
			spawn("native-thread-42", "child-1", "root-turn")  // reliable terminal replay
			spawn("native-thread-42", "child-1", "second-run") // distinct native turn
			spawn("native-thread-42", "child-1", "root-turn")  // cache-eviction probe
			spawn("child-1", "grand", "child-turn")            // confirms previously associated grandchild
			relation("grand", "native-thread-42")              // retain original parent
			relation("child-1", "grand")                       // cycle
			for i := 0; i < 2; i++ {
				emit("item/completed", `{"threadId":"grand","turnId":"g-turn","item":{"type":"futureItem","id":"unknown-occurrence","payload":null}}`)
			}
			emit("item/started", `{"threadId":"grand","turnId":"g-turn","item":{"type":"subAgentActivity","id":"activity","agentThreadId":"other","agentPath":"/guessed/child","kind":"future-kind"}}`)
			fmt.Fprintln(os.Stdout, treeChildRaw) // same delta, no occurrence ID
			// An early terminal event flushed from pending must dedup a later replay.
			spawn("orphan", "leaf", "o-turn")
			relation("orphan", "grand")
			spawn("orphan", "leaf", "o-turn")
		}
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "tree-conflict":
		relation("grand", "native-thread-42")
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{}`)
	case "tree-link":
		relation("child-1", "native-thread-42")
		relation("late", "native-thread-42")
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}

func handleLifecycleTreePeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if mode != "tree-lifecycle" && mode != "tree-history" {
		return false
	}
	emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
	switch m.Method {
	case "turn/start":
		emit("item/completed", treeSpawnParams("spawnAgent", "completed"))
		if mode == "tree-history" {
			p := strings.ReplaceAll(treeSpawnParams("spawnAgent", "completed"), `"child-1","child-1","child-2"`, `"live-child","known-unlinked"`)
			p = strings.ReplaceAll(p, "spawn-1", "spawn-live")
			emit("item/completed", p)
		} else {
			// wait_agent keeps the parent active. Parent idle/completed concerns that
			// thread only; the independently active child continues on this connection.
			emit("thread/status/changed", `{"threadId":"native-thread-42","status":{"type":"active","activeFlags":[]}}`)
			emit("thread/status/changed", `{"threadId":"child-1","status":{"type":"active","activeFlags":[]}}`)
			emit("thread/status/changed", `{"threadId":"native-thread-42","status":{"type":"idle"}}`)
			emit("turn/completed", `{"threadId":"native-thread-42","turn":{"id":"root-turn","items":[],"status":"completed","error":null}}`)
			fmt.Fprintln(os.Stdout, treeChildRaw)
			for _, kind := range []string{"completed", "interrupted"} {
				emit("item/completed", `{"threadId":"native-thread-42","turnId":"root-turn","completedAtMs":9,"item":{"type":"subAgentActivity","id":"activity-`+kind+`","agentThreadId":"child-1","agentPath":"/display-only","kind":"`+kind+`"}}`)
			}
			emit("turn/completed", `{"threadId":"child-1","turn":{"id":"child-turn","items":[],"status":"interrupted","error":null}}`)
			emit("turn/started", `{"threadId":"child-1","turn":{"id":"next-child-turn","items":[],"status":"inProgress"}}`)
		}
		emit("future/done", `{"threadId":"native-thread-42"}`)
		send(m.ID, `{"turn":`+nativeTurn+`}`)
	case "tree-step":
		fmt.Fprintln(os.Stdout, treeChildRaw)
		send(m.ID, `{}`)
	default:
		return false
	}
	return true
}

func treeFrame(method, params string) string {
	return fmt.Sprintf(`{"method":%q,"params":%s}`, method, params)
}

func v2Activity(parent, child, item, kind string) string {
	return fmt.Sprintf(`{"threadId":%q,"turnId":"parent-turn","completedAtMs":3,"item":{"type":"subAgentActivity","id":%q,"kind":%q,"agentThreadId":%q,"agentPath":"/display/only","model":"selected-model","reasoningEffort":"high","future":[null,9007199254740993]}}`, parent, item, kind, child)
}
