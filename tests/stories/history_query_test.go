package stories_test

func (s *CommonStoriesSuite) TestIndependentHistoryBrowser() {
	s.Run("US-014/codex", func() { codexIndependentHistory(s.T()) })
	s.Run("US-014/claude", func() { claudeIndependentHistory(s.T()) })
}

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

func (s *CommonStoriesSuite) TestClaudeSessionMetadata()        { checkClaudeSessionMetadata(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataScopeAndPages()  { checkClaudeMetadataScopeAndPages(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataSamples()        { checkClaudeMetadataSamples(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataFailures()       { checkClaudeMetadataFailures(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataDefaultPage()    { checkClaudeMetadataDefaultPage(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataSampleEdges()    { checkClaudeMetadataSampleEdges(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataIncompleteTail() { checkClaudeMetadataIncompleteTail(s) }
func (s *CommonStoriesSuite) TestClaudeMetadataNestedSampleProvenance() {
	checkClaudeMetadataNestedSampleProvenance(s)
}

func (s *CommonStoriesSuite) TestClaudeMetadataTailLineBoundary() {
	checkClaudeMetadataTailLineBoundary(s)
}

func (s *CommonStoriesSuite) TestClaudeNonMessageRequestMetadata() {
	checkClaudeNonMessageRequestMetadata(s)
}

func (s *CommonStoriesSuite) TestClaudeRequestMetadataGuards() {
	checkClaudeRequestMetadataGuards(s)
}

func (s *CommonStoriesSuite) TestIndependentSubagentHistory() {
	s.Run("H1-no-session", func() { checkClaudeIndependentChildHistory(s) })
	s.Run("H1-live-coexistence", func() { checkClaudeChildHistoryAlongsideSession(s) })
}
