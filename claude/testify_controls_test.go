// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"github.com/stretchr/testify/suite"
	"testing"
)

// controlInteractionSuite owns the clients involved in each permission, question,
// transport and cancellation scenario. Subtests keep their own testing.T so the
// existing fixture cleanup still runs as soon as each table row completes.
// Goroutines exchange outcomes with the test; no suite method runs in parallel.
type controlInteractionSuite struct {
	suite.Suite
	clients []*Client
}

func TestControlInteractions(t *testing.T) { suite.Run(t, new(controlInteractionSuite)) }

func (s *controlInteractionSuite) SetupTest() { s.clients = nil }

func (s *controlInteractionSuite) openFixture(t *testing.T, mode string, configure ...func(*Options)) *Client {
	t.Helper()
	c := openFixture(t, mode, configure...)
	s.clients = append(s.clients, c)
	return c
}

func (s *controlInteractionSuite) TearDownTest() {
	// Some scenarios deliberately terminate with a protocol/resource failure;
	// their bodies assert the exact Close/Err cause. Cleanup only releases clients.
	for i := len(s.clients) - 1; i >= 0; i-- {
		_ = s.clients[i].Close()
	}
	s.clients = nil
}
