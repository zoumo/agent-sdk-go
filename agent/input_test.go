package agent_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInputValidationRejectsEmptyAndInvalidContent(t *testing.T) {
	emptyText := ""
	negativeSize := int64(-1)
	invalidPriority := 1.1
	nanPriority := math.NaN()
	infPriority := math.Inf(1)
	invalidLastModified := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	blob := "aGVsbG8="
	text := "hello"

	tests := []struct {
		name  string
		input agent.Input
	}{
		{name: "empty"},
		{
			name:  "nil block",
			input: agent.Input{Content: []agent.ContentBlock{nil}},
		},
		{
			name: "invalid image Base64",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.ImageBlock{Data: "not-base64", MIMEType: "image/png"},
			}},
		},
		{
			name: "resource without body",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.ResourceBlock{Resource: agent.EmbeddedResource{URI: "file:///tmp/a"}},
			}},
		},
		{
			name: "resource with both bodies",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.ResourceBlock{Resource: agent.EmbeddedResource{
					URI:  "file:///tmp/a",
					Text: &text,
					Blob: &blob,
				}},
			}},
		},
		{
			name: "negative resource link size",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.ResourceLinkBlock{
					URI:  "file:///tmp/a",
					Name: "a",
					Size: &negativeSize,
				},
			}},
		},
		{
			name: "annotation priority outside range",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.TextBlock{
					Text:        "hello",
					Annotations: &agent.Annotations{Priority: &invalidPriority},
				},
			}},
		},
		{
			name: "annotation priority is NaN",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.TextBlock{
					Text:        "hello",
					Annotations: &agent.Annotations{Priority: &nanPriority},
				},
			}},
		},
		{
			name: "annotation priority is infinite",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.TextBlock{
					Text:        "hello",
					Annotations: &agent.Annotations{Priority: &infPriority},
				},
			}},
		},
		{
			name: "annotation last modified cannot encode",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.TextBlock{
					Text:        "hello",
					Annotations: &agent.Annotations{LastModified: &invalidLastModified},
				},
			}},
		},
		{
			name: "unknown annotation audience",
			input: agent.Input{Content: []agent.ContentBlock{
				agent.TextBlock{
					Text:        emptyText,
					Annotations: &agent.Annotations{Audience: []string{"system"}},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestInputAndContentBlocksRoundTripJSON(t *testing.T) {
	uri := "https://example.test/image.png"
	mimeType := "text/plain"
	text := "embedded"
	title := "Example"
	description := "Example resource"
	size := int64(42)
	priority := 0.5
	lastModified := time.Date(2026, 11, 7, 8, 30, 0, 0, time.UTC)

	original := agent.Input{Content: []agent.ContentBlock{
		agent.TextBlock{
			Text: "hello",
			Annotations: &agent.Annotations{
				Audience:     []string{"user", "assistant"},
				Priority:     &priority,
				LastModified: &lastModified,
			},
		},
		agent.ImageBlock{
			Data:     "aGVsbG8=",
			MIMEType: "image/png",
			URI:      &uri,
		},
		agent.AudioBlock{
			Data:     "aGVsbG8=",
			MIMEType: "audio/wav",
		},
		agent.ResourceBlock{
			Resource: agent.EmbeddedResource{
				URI:      "file:///tmp/example.txt",
				MIMEType: &mimeType,
				Text:     &text,
			},
		},
		agent.ResourceLinkBlock{
			URI:         "file:///tmp/example.txt",
			Name:        "example.txt",
			MIMEType:    &mimeType,
			Title:       &title,
			Description: &description,
			Size:        &size,
		},
	}}
	require.NoError(t, original.Validate())

	encoded, err := json.Marshal(original)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"content": [
			{
				"type": "text",
				"text": "hello",
				"annotations": {
					"audience": ["user", "assistant"],
					"priority": 0.5,
					"lastModified": "2026-11-07T08:30:00Z"
				}
			},
			{
				"type": "image",
				"data": "aGVsbG8=",
				"mimeType": "image/png",
				"uri": "https://example.test/image.png"
			},
			{
				"type": "audio",
				"data": "aGVsbG8=",
				"mimeType": "audio/wav"
			},
			{
				"type": "resource",
				"resource": {
					"uri": "file:///tmp/example.txt",
					"mimeType": "text/plain",
					"text": "embedded"
				}
			},
			{
				"type": "resource_link",
				"uri": "file:///tmp/example.txt",
				"name": "example.txt",
				"mimeType": "text/plain",
				"title": "Example",
				"description": "Example resource",
				"size": 42
			}
		]
	}`, string(encoded))

	var decoded agent.Input
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, original, decoded)
	require.NoError(t, decoded.Validate())
}

func TestInputJSONRejectsUnknownContentKind(t *testing.T) {
	var input agent.Input

	err := json.Unmarshal([]byte(`{"content":[{"type":"video","data":"aGVsbG8="}]}`), &input)

	require.Error(t, err)
}

func TestInputJSONRejectsMissingOrNullRequiredContentFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "missing text",
			raw:  `{"content":[{"type":"text"}]}`,
		},
		{
			name: "null text",
			raw:  `{"content":[{"type":"text","text":null}]}`,
		},
		{
			name: "missing image data",
			raw:  `{"content":[{"type":"image","mimeType":"image/png"}]}`,
		},
		{
			name: "null image data",
			raw:  `{"content":[{"type":"image","data":null,"mimeType":"image/png"}]}`,
		},
		{
			name: "missing audio data",
			raw:  `{"content":[{"type":"audio","mimeType":"audio/wav"}]}`,
		},
		{
			name: "null audio data",
			raw:  `{"content":[{"type":"audio","data":null,"mimeType":"audio/wav"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input agent.Input

			err := json.Unmarshal([]byte(tt.raw), &input)

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}
