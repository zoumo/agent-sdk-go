package uuidv4

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewReturnsDistinctRFC4122Version4Identifiers(t *testing.T) {
	first, err := New()
	require.NoError(t, err)
	second, err := New()
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	for _, value := range []string{first, second} {
		require.Len(t, value, 36)
		assert.Equal(t, byte('4'), value[14])
		assert.Contains(t, "89ab", strings.ToLower(string(value[19])))
		for index, character := range value {
			if index == 8 || index == 13 || index == 18 || index == 23 {
				assert.Equal(t, byte('-'), byte(character))
				continue
			}
			assert.True(t, strings.ContainsRune("0123456789abcdefABCDEF", character))
		}
	}
}
