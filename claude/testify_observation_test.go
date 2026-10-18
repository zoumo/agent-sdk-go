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
	client *Client
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
	case "TestMissingPermissionHandlerDeniesAndUnknownControlErrors":
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
	client *Client
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

type recordedObservationSuite struct {
	suite.Suite
}

func TestRecordedObservationSuite(t *testing.T) { suite.Run(t, new(recordedObservationSuite)) }

type treeObservationSuite struct {
	suite.Suite
	client *Client
}

func TestTreeObservationSuite(t *testing.T) { suite.Run(t, new(treeObservationSuite)) }
func (s *treeObservationSuite) SetupTest() {
	s.client = nil
	name := s.T().Name()
	name = name[strings.LastIndex(name, "/")+1:]
	switch name {
	case "TestSharedLineageAliasMergeKeepsDescendantRouting":
		s.client = openTreeFixture(s.T())
	case "TestTreeSingleLayerNativeLosslessAndAssociationOrder":
		s.client = openTreeFixture(s.T())
	case "TestTreeUnresolvedAndNonAgentTask":
		s.client = openTreeFixture(s.T())
	case "TestTreeUnknownSystemSubtypeNeedsSourceEvidence":
		s.client = openTreeFixture(s.T())
	case "TestTreeCreatedNestedParallelAndEntityDedup":
		s.client = openTreeFixture(s.T())
	case "TestTreeCreatedSurvivesAliasMerge":
		s.client = openTreeFixture(s.T())
	case "TestTreeCreatedDuplicateInitBoundary":
		s.client = openTreeFixture(s.T())
	case "TestTreeModernRecursiveRunsAndLateFrames":
		s.client = openTreeFixture(s.T())
	case "TestTreeLegacyRecursiveToolOwnershipAndEnrichment":
		s.client = openTreeFixture(s.T())
	case "TestTreeChildBeforeRelationUnknownParentAndNoDepthGuess":
		s.client = openTreeFixture(s.T())
	case "TestTreeNonAgentsHooksAndFailedSpawnNeverCreated":
		s.client = openTreeFixture(s.T())
	case "TestTreeUUIDDedupFullScopeAndNoIDPreservation":
		s.client = openTreeFixture(s.T())
	case "TestTreeLineageConflictsDoNotRebind":
		s.client = openTreeFixture(s.T())
	case "TestTreeIndependentAliasesReconcileWithBridgeEvidence":
		s.client = openTreeFixture(s.T())
	case "TestTreeStaleMetadataCannotPoisonCurrentRun":
		s.client = openTreeFixture(s.T())
	case "TestTreeKnownNonAgentToolDoesNotCreateLegacyAgent":
		s.client = openTreeFixture(s.T())
	case "TestTreeConflictingToolAliasDoesNotPoisonTaskKey":
		s.client = openTreeFixture(s.T())
	case "TestTreeResumeNewToolAndDuplicateRelationships":
		s.client = openTreeFixture(s.T())
	case "TestTreeNoRunUUIDNotSuppressedAcrossObservedRunBoundary":
		s.client = openTreeFixture(s.T())
	case "TestTreeRunTerminalsAndBackgroundLevel":
		s.client = openTreeFixture(s.T())
	case "TestTreeUnclassifiedRunCannotRegressAgentMetadata":
		s.client = openTreeFixture(s.T())
	}
}
