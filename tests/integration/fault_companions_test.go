package integration_test

func (s *NativeIntegrationSuite) TestTaskFailureDrain() {
	checkTaskFailureDrain(s.T())
}

func (s *NativeIntegrationSuite) TestQuestionDrain() {
	checkQuestionDrain(s.T())
}

func (s *NativeIntegrationSuite) TestFaultClosePendingRequest() {
	s.Run("codex", func() { checkCodexFaultClosePendingRequest(s.T()) })
	s.Run("claude", func() { checkClaudeFaultClosePendingRequest(s.T()) })
}

func (s *NativeIntegrationSuite) TestReplayFinalResult() {
	s.Run("codex", func() { checkReplayFinalResult(s.T(), "codex") })
	s.Run("claude", func() { checkReplayFinalResult(s.T(), "claude") })
}
