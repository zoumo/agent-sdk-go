package stories_test

func (s *CommonStoriesSuite) TestClaudeIndependentMessages()  { checkClaudeIndependentMessages(s) }
func (s *CommonStoriesSuite) TestClaudeMessageSelection()     { checkClaudeMessageSelection(s) }
func (s *CommonStoriesSuite) TestClaudeMessageQueryFailures() { checkClaudeMessageQueryFailures(s) }
func (s *CommonStoriesSuite) TestClaudeQueryScope()           { checkClaudeQueryScope(s) }
func (s *CommonStoriesSuite) TestClaudeParallelStrays()       { checkClaudeParallelStrays(s) }
func (s *CommonStoriesSuite) TestClaudeQueryDefaultReturnBound() {
	checkClaudeQueryDefaultReturnBound(s)
}
func (s *CommonStoriesSuite) TestClaudeQueryAlongsideSession() { checkClaudeQueryAlongsideSession(s) }
func (s *CommonStoriesSuite) TestClaudeNullToolIdentity()      { checkClaudeNullToolIdentity(s) }
func (s *CommonStoriesSuite) TestClaudeRecoveredTimestampOrder() {
	checkClaudeRecoveredTimestampOrder(s)
}
