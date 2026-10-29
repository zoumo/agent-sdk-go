package integration_test

func (s *NativeIntegrationSuite) TestIndependentHistoryBrowser() {
	s.Run("history-query/codex", func() { codexIndependentHistory(s.T()) })
	s.Run("history-query/claude", func() { claudeIndependentHistory(s.T()) })
}

func (s *NativeIntegrationSuite) TestClaudeIndependentMessages()  { checkClaudeIndependentMessages(s) }
func (s *NativeIntegrationSuite) TestClaudeMessageSelection()     { checkClaudeMessageSelection(s) }
func (s *NativeIntegrationSuite) TestClaudeMessageQueryFailures() { checkClaudeMessageQueryFailures(s) }
func (s *NativeIntegrationSuite) TestClaudeQueryScope()           { checkClaudeQueryScope(s) }
func (s *NativeIntegrationSuite) TestClaudeParallelStrays()       { checkClaudeParallelStrays(s) }
func (s *NativeIntegrationSuite) TestClaudeQueryDefaultReturnBound() {
	checkClaudeQueryDefaultReturnBound(s)
}
func (s *NativeIntegrationSuite) TestClaudeQueryAlongsideSession() {
	checkClaudeQueryAlongsideSession(s)
}
func (s *NativeIntegrationSuite) TestClaudeNullToolIdentity() { checkClaudeNullToolIdentity(s) }
func (s *NativeIntegrationSuite) TestClaudeRecoveredTimestampOrder() {
	checkClaudeRecoveredTimestampOrder(s)
}

func (s *NativeIntegrationSuite) TestClaudeSessionMetadata() { checkClaudeSessionMetadata(s) }
func (s *NativeIntegrationSuite) TestClaudeMetadataScopeAndPages() {
	checkClaudeMetadataScopeAndPages(s)
}
func (s *NativeIntegrationSuite) TestClaudeMetadataSamples()     { checkClaudeMetadataSamples(s) }
func (s *NativeIntegrationSuite) TestClaudeMetadataFailures()    { checkClaudeMetadataFailures(s) }
func (s *NativeIntegrationSuite) TestClaudeMetadataDefaultPage() { checkClaudeMetadataDefaultPage(s) }
func (s *NativeIntegrationSuite) TestClaudeMetadataSampleEdges() { checkClaudeMetadataSampleEdges(s) }
func (s *NativeIntegrationSuite) TestClaudeMetadataIncompleteTail() {
	checkClaudeMetadataIncompleteTail(s)
}
func (s *NativeIntegrationSuite) TestClaudeMetadataNestedSampleProvenance() {
	checkClaudeMetadataNestedSampleProvenance(s)
}

func (s *NativeIntegrationSuite) TestClaudeMetadataTailLineBoundary() {
	checkClaudeMetadataTailLineBoundary(s)
}

func (s *NativeIntegrationSuite) TestClaudeNonMessageRequestMetadata() {
	checkClaudeNonMessageRequestMetadata(s)
}

func (s *NativeIntegrationSuite) TestClaudeRequestMetadataGuards() {
	checkClaudeRequestMetadataGuards(s)
}
