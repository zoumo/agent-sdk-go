// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// Suites share isolated subprocess setup. They deliberately run sequentially;
// native concurrent stress tests retain their goroutines and synchronization.
type clientProtocolSuite struct {
	suite.Suite
	client *interaction
}

func TestClientProtocolSuite(t *testing.T) { suite.Run(t, new(clientProtocolSuite)) }
func (s *clientProtocolSuite) SetupTest() {
	s.client = nil
	name := s.T().Name()
	name = name[strings.LastIndex(name, "/")+1:]
	switch name {
	case "TestTypedViewsAndLosslessUnknowns":
		s.client = openFixture(s.T(), "frames")
	case "TestFragmentedUnicodeAndLargeFrame":
		s.client = openFixture(s.T(), "fragmented")
	case "TestUnexpectedEOFAfterAcceptedEvent":
		s.client = openFixture(s.T(), "eof")
	case "TestPermissionWaitsAndUnknownControlErrors":
		s.client = openFixture(s.T(), "permissions")
	case "TestReceiveCancellationDoesNotCancelSession":
		s.client = openFixture(s.T(), "echo")
	case "TestSendCancellationBeforeWriteDoesNotClose":
		s.client = openFixture(s.T(), "echo")
	case "TestCloseRetainsBufferedEvents":
		s.client = openFixture(s.T(), "echo")
	case "TestInvalidUTF8IsProtocolError":
		s.client = openFixture(s.T(), "invalid_utf8")
	case "TestCloseAfterDoneRetainsFinalProtocolError":
		s.client = openFixture(s.T(), "truncated")
	}
}

type sessionControlSuite struct {
	suite.Suite
	client *interaction
}

func TestSessionControlSuite(t *testing.T) { suite.Run(t, new(sessionControlSuite)) }
func (s *sessionControlSuite) SetupTest() {
	s.client = nil
	name := s.T().Name()
	name = name[strings.LastIndex(name, "/")+1:]
	switch name {
	case "TestSessionIdentityObservedBeforeReceiveAndWithoutSystemInit":
		s.client = openFixture(s.T(), "session_identity_at_initialize")
	case "TestInterruptCorrelatesOutOfOrderAndUnknownResponses":
		s.client = openFixture(s.T(), "session_controls")
	case "TestInterruptAcknowledgmentIsSeparateFromTurnResult":
		s.client = openFixture(s.T(), "session_controls")
	case "TestInterruptImmediateAcknowledgments":
		s.client = openFixture(s.T(), "session_immediate")
	case "TestInterruptWriteFailureWakesOtherWaiters":
		s.client = openFixture(s.T(), "session_controls")
	}
}

type syntheticObservationSuite struct {
	suite.Suite
}

func TestSyntheticObservationSuite(t *testing.T) { suite.Run(t, new(syntheticObservationSuite)) }
