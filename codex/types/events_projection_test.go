package types_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestEventDoesNotExposeHistoryProjection(t *testing.T) {
	_, found := reflect.TypeFor[types.Event]().FieldByName("History")
	require.False(t, found, "Event.History must not expose a saved-history event projection")
}
