package agentcodex

import (
	"mime"
	"net/url"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func encodeInput(input agent.Input, allowMedia bool) (codex.Input, error) {
	if err := input.Validate(); err != nil {
		return codex.Input{}, err
	}
	parts := make([]codextypes.UserInput, 0, len(input.Content))
	for _, block := range input.Content {
		part, err := encodeInputBlock(block, allowMedia)
		if err != nil {
			return codex.Input{}, err
		}
		parts = append(parts, part)
	}
	return codex.Input{Input: parts}, nil
}

func encodeInputBlock(block agent.ContentBlock, allowMedia bool) (codextypes.UserInput, error) {
	switch {
	case block.Text != nil:
		if block.Text.Annotations != nil {
			return codextypes.UserInput{}, adapterError(
				agent.ErrorUnsupported, agent.ErrorReasonTextAnnotationsUnsupported,
				"Codex input does not support text annotations", nil,
			)
		}
		return codextypes.UserInput{Text: &codextypes.TextUserInput{Text: block.Text.Text}}, nil
	case allowMedia && block.Image != nil:
		image := block.Image
		if uri := dataURL("image/", image.MIMEType, image.Data); uri != "" && image.Annotations == nil {
			return imageInput(uri), nil
		}
	case allowMedia && block.Audio != nil:
		audio := block.Audio
		if uri := dataURL("audio/", audio.MIMEType, audio.Data); uri != "" && audio.Annotations == nil {
			return codextypes.UserInput{Audio: &codextypes.AudioUserInput{URL: uri}}, nil
		}
	case allowMedia && block.ResourceLink != nil:
		link := block.ResourceLink
		if link.Annotations == nil && link.MIMEType != nil && mediaType("image/", *link.MIMEType) != "" {
			uri, err := url.Parse(link.URI)
			if err == nil && uri.Hostname() != "" && (uri.Scheme == "https" || uri.Scheme == "http") {
				return imageInput(link.URI), nil
			}
		}
	}
	return codextypes.UserInput{}, adapterError(
		agent.ErrorUnsupported, agent.ErrorReasonContentBlockUnsupported,
		"Codex does not support this input block", nil,
	)
}

func imageInput(uri string) codextypes.UserInput {
	return codextypes.UserInput{Image: &codextypes.ImageUserInput{URL: codextypes.Some(uri)}}
}

func dataURL(prefix, mimeType, data string) string {
	media := mediaType(prefix, mimeType)
	if media == "" {
		return ""
	}
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r", ""), "\n", "")
	return "data:" + media + ";base64," + data
}

func mediaType(prefix, value string) string {
	media, params, err := mime.ParseMediaType(value)
	subtype, matches := strings.CutPrefix(media, prefix)
	if err != nil || len(params) != 0 || !matches || subtype == "*" || url.PathEscape(subtype) != subtype {
		return ""
	}
	return media
}
