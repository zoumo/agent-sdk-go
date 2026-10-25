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
