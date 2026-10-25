package stories_test

func (s *CommonStoriesSuite) TestRecordedFaultCompanion() {
	checkRecordedFaultCompanion(s.T())
}

func (s *CommonStoriesSuite) TestRecordedQuestionFaultCompanion() {
	checkRecordedQuestionFaultCompanion(s.T())
}

func (s *CommonStoriesSuite) TestFaultClosePendingRequest() {
	s.Run("codex", func() { checkCodexFaultClosePendingRequest(s.T()) })
	s.Run("claude", func() { checkClaudeFaultClosePendingRequest(s.T()) })
}

func (s *CommonStoriesSuite) TestReplayFinalResult() {
	s.Run("codex", func() { checkReplayFinalResult(s.T(), "codex") })
	s.Run("claude", func() { checkReplayFinalResult(s.T(), "claude") })
}
