package codex_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	codex "github.com/zoumo/agent-sdk-go/codex"
)

// observationSuite owns clients created by each scenario. Transport table
// subtests retain their own cleanup scope, so one transport cannot leak into the
// next. Concurrent lifecycle stress tests remain native testing tests.
type observationSuite struct {
	suite.Suite
	clients []fixture
}

func TestObservationSuite(t *testing.T)  { suite.Run(t, new(observationSuite)) }
func (obs *observationSuite) SetupTest() { obs.clients = nil }
func (obs *observationSuite) TearDownTest() {
	for _, f := range obs.clients {
		obs.NoError(f.c.Close())
	}
}

func (obs *observationSuite) fixture(t *testing.T, mode string, limits codex.Limits) fixture {
	t.Helper()
	if t != obs.T() {
		return startFake(t, mode, limits)
	}
	f, err := openFake(t, mode, limits)
	require.NoError(t, err)
	obs.clients = append(obs.clients, f)
	return f
}

// observationCase reuses the same scenario with the remote parity table's
// transport selection while preserving the suite's setup and cleanup lifecycle.
func observationCase(run func(*observationSuite)) func(*testing.T) {
	return func(t *testing.T) {
		s := new(observationSuite)
		s.SetT(t)
		s.SetupTest()
		defer s.TearDownTest()
		run(s)
	}
}
