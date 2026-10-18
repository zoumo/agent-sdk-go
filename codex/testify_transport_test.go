package codex_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	codex "github.com/zoumo/agent-sdk-go/codex"
)

// Each suite test receives a fresh peer and its native client. startFake owns
// shutdown cleanup even when a prerequisite assertion aborts the test.

type stdioLifecycleSuite struct {
	suite.Suite
	peer fixture
}

func (s *stdioLifecycleSuite) SetupTest()               { s.peer = startFake(s.T(), "normal", codex.Limits{}) }
func TestStdioLifecycleSuite(t *testing.T)              { suite.Run(t, new(stdioLifecycleSuite)) }
func (s *stdioLifecycleSuite) TestOwnedStdioLifecycle() { runOwnedStdioLifecycle(s.T(), s.peer) }
func (s *stdioLifecycleSuite) TestCancellationAndLateResponse() {
	runCancellationAndLateResponse(s.T(), s.peer)
}
func (s *stdioLifecycleSuite) TestRPCErrorNoReplay() { runRPCErrorNoReplay(s.T(), s.peer) }
func (s *stdioLifecycleSuite) TestConnectionLossIsUnknown() {
	runConnectionLossIsUnknown(s.T(), s.peer)
}

type serverRequestLifecycleSuite struct {
	suite.Suite
	peer fixture
}

func (s *serverRequestLifecycleSuite) SetupTest() {
	s.peer = startFake(s.T(), "request-normal", codex.Limits{})
}
func TestServerRequestLifecycleSuite(t *testing.T) { suite.Run(t, new(serverRequestLifecycleSuite)) }
func (s *serverRequestLifecycleSuite) TestServerRequestIDsAndNativeReplyEnvelopes() {
	runServerRequestIDsAndNativeReplyEnvelopes(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestServerRequestRawAndTypedCopies() {
	runServerRequestRawAndTypedCopies(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestResolvedServerRequestUsesExactIDAndKeepsEvent() {
	runResolvedServerRequestUsesExactIDAndKeepsEvent(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestServerRequestCloseInvalidatesAndUnblocks() {
	runServerRequestCloseInvalidatesAndUnblocks(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestConcurrentServerRequestAnswerIsOneShot() {
	runConcurrentServerRequestAnswerIsOneShot(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestServerRequestDuplicateActiveIDTerminates() {
	runServerRequestDuplicateActiveIDTerminates(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestMalformedOrUnrelatedResolutionKeepsHandle() {
	runMalformedOrUnrelatedResolutionKeepsHandle(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestConcurrentAnswerAndCancelHaveOneWinner() {
	runConcurrentAnswerAndCancelHaveOneWinner(s.T(), s.peer)
}
func (s *serverRequestLifecycleSuite) TestConcurrentAnswerAndNativeResolutionHaveOneOutcome() {
	runConcurrentAnswerAndNativeResolutionHaveOneOutcome(s.T(), s.peer)
}

// Peer callbacks never invoke testing or FailNow. Their errors are collected
// under a mutex and asserted by the owning test during cleanup, after peers
// registered later have shut down.
func collectTransportPeerErrors(t *testing.T) func(string, ...any) {
	t.Helper()
	var mu sync.Mutex
	var reports []error
	t.Cleanup(func() {
		mu.Lock()
		copied := append([]error(nil), reports...)
		mu.Unlock()
		for _, err := range copied {
			assert.NoError(t, err)
		}
	})
	return func(format string, args ...any) {
		mu.Lock()
		reports = append(reports, fmt.Errorf(format, args...))
		mu.Unlock()
	}
}
