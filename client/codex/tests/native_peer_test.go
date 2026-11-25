package codex_test

import (
	"testing"

	"github.com/zoumo/agent-sdk-go/internal/testsupport/nativepeer"
)

func TestMain(m *testing.M) {
	nativepeer.TestMain(m, func(provider, scenario string) error {
		switch scenario {
		case "child-detail":
			return childDetailPeer()
		case "history-query":
			return historyQueryScenarioPeer()
		default:
			return nativepeer.Session(provider, scenario)
		}
	})
}
