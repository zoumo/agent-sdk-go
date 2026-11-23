package agentclaude

import (
	"encoding/json"
	"mime"
	"net/url"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

func promptContent(input agent.Input) (claudetypes.Content, error) {
	if err := input.Validate(); err != nil {
		return claudetypes.Content{}, err
	}
	if len(input.Content) == 1 && input.Content[0].Text != nil {
		text, err := plainText(input.Content[0].Text)
		return claudetypes.Content{Text: &text}, err
	}

	blocks := make([]claudetypes.ContentBlock, 0, len(input.Content))
	for _, block := range input.Content {
		native, err := encodePromptBlock(block)
		if err != nil {
			return claudetypes.Content{}, err
		}
		blocks = append(blocks, native)
	}
	return claudetypes.Content{Blocks: blocks}, nil
}

func encodePromptBlock(block agent.ContentBlock) (claudetypes.ContentBlock, error) {
	switch {
	case block.Text != nil:
		text, err := plainText(block.Text)
		return claudetypes.ContentBlock{Type: "text", Text: &claudetypes.TextBlock{Text: text}}, err
	case block.Image != nil:
		image := block.Image
		if image.Annotations == nil && imageMediaType(image.MIMEType) {
			return mediaBlock("image", base64Source(image.MIMEType, image.Data), nil, nil)
		}
	case block.EmbeddedResource != nil:
		embedded := block.EmbeddedResource
		resource := embedded.Resource
		if embedded.Annotations == nil {
			switch {
			case resource.Text != nil && (resource.MIMEType == nil || *resource.MIMEType == "text/plain"):
				source := mediaSource{Type: "text", MediaType: "text/plain", Data: resource.Text}
				return mediaBlock("document", source, nil, nil)
			case resource.Blob != nil && resource.MIMEType != nil && *resource.MIMEType == "application/pdf":
				return mediaBlock("document", base64Source(*resource.MIMEType, *resource.Blob), nil, nil)
			}
		}
	case block.ResourceLink != nil:
		return resourceLinkBlock(block.ResourceLink)
	}
	return claudetypes.ContentBlock{}, unsupportedPromptBlock()
}

func resourceLinkBlock(link *agent.ContentBlockResourceLink) (claudetypes.ContentBlock, error) {
	if link.Annotations != nil {
		return claudetypes.ContentBlock{}, unsupportedPromptBlock()
	}
	if link.MIMEType == nil {
		return claudetypes.ContentBlock{Type: "text", Text: &claudetypes.TextBlock{Text: link.URI}}, nil
	}
	uri, err := url.Parse(link.URI)
	if err != nil || uri.Hostname() == "" || (uri.Scheme != "https" && uri.Scheme != "http") {
		return claudetypes.ContentBlock{}, unsupportedPromptBlock()
	}
	source := mediaSource{Type: "url", URL: link.URI}
	switch {
	case imageMediaType(*link.MIMEType):
		return mediaBlock("image", source, nil, nil)
	case *link.MIMEType == "application/pdf":
		title := link.Title
		if title == nil {
			title = &link.Name
		}
		return mediaBlock("document", source, title, link.Description)
	}
	if media, _, err := mime.ParseMediaType(*link.MIMEType); err == nil && media == "text/html" {
		return claudetypes.ContentBlock{Type: "text", Text: &claudetypes.TextBlock{Text: link.URI}}, nil
	}
	return claudetypes.ContentBlock{}, unsupportedPromptBlock()
}

type mediaSource struct {
	Type      string  `json:"type"`
	MediaType string  `json:"media_type,omitempty"`
	Data      *string `json:"data,omitempty"`
	URL       string  `json:"url,omitempty"`
}

func base64Source(mimeType, data string) mediaSource {
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r", ""), "\n", "")
	return mediaSource{Type: "base64", MediaType: mimeType, Data: &data}
}

func mediaBlock(kind string, source mediaSource, title, description *string) (claudetypes.ContentBlock, error) {
	// Native Claude content retains image and document blocks in its Raw branch.
	raw, err := json.Marshal(struct {
		Type    string      `json:"type"`
		Source  mediaSource `json:"source"`
		Title   *string     `json:"title,omitempty"`
		Context *string     `json:"context,omitempty"`
	}{Type: kind, Source: source, Title: title, Context: description})
	return claudetypes.ContentBlock{Type: kind, Raw: raw}, err
}

func imageMediaType(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func textInput(input agent.Input) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	if len(input.Content) != 1 {
		return "", adapterError(
			agent.ErrorUnsupported, ErrorReasonMultipleContentBlocksUnsupported,
			"Claude context injection supports exactly one content block", nil,
		)
	}
	if input.Content[0].Text == nil {
		return "", unsupportedPromptBlock()
	}
	return plainText(input.Content[0].Text)
}

func plainText(block *agent.ContentBlockText) (string, error) {
	if block.Annotations != nil {
		return "", adapterError(
			agent.ErrorUnsupported, agent.ErrorReasonTextAnnotationsUnsupported,
			"Claude input does not support text annotations", nil,
		)
	}
	return block.Text, nil
}

func unsupportedPromptBlock() error {
	return adapterError(
		agent.ErrorUnsupported, agent.ErrorReasonContentBlockUnsupported,
		"Claude does not support this input block", nil,
	)
}
