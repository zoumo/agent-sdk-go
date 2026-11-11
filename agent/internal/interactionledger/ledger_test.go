package interactionledger_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
)

func TestLedgerAllocatesOpaqueUniqueReferencesAndMonotonicSequence(t *testing.T) {
	const count = 64
	ledger := interactionledger.New("codex-")

	refs := make([]string, count)
	sequences := make([]uint64, count)
	failures := make(chan error, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range count {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			ref, sequence, err := ledger.Allocate()
			if err != nil {
				failures <- fmt.Errorf("allocate reference %d: %w", index, err)
				return
			}
			refs[index] = string(ref)
			sequences[index] = sequence
		}(index)
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	seen := make(map[string]struct{}, count)
	for _, ref := range refs {
		require.NotEmpty(t, ref)
		assert.True(t, strings.HasPrefix(ref, "codex-"))
		_, duplicate := seen[ref]
		assert.False(t, duplicate, "duplicate interaction reference %q", ref)
		seen[ref] = struct{}{}
	}

	slices.Sort(sequences)
	for index, sequence := range sequences {
		assert.Equal(t, uint64(index+1), sequence)
	}
}
