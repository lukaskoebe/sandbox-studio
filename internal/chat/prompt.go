package chat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

// MaxAttachments caps the attachments of one prompt, in total.
const MaxAttachments = 10 << 20

// MaxMentions caps the files one prompt mentions.
const MaxMentions = 50

// promptBlocks turns a prompt into ACP content: the text, a link to each file it mentions,
// then images as image blocks and other files as embedded resources, where the agent
// accepts them.
func promptBlocks(caps acp.AgentCapabilities, p Prompt) ([]acp.ContentBlock, []Attachment, error) {
	var blocks []acp.ContentBlock
	if strings.TrimSpace(p.Text) != "" {
		blocks = append(blocks, acp.ContentBlock{Type: "text", Text: p.Text})
	}
	if len(p.Mentions) > MaxMentions {
		return nil, nil, fmt.Errorf("%w: a message mentions at most %d files", ErrInvalid, MaxMentions)
	}
	for _, m := range p.Mentions {
		link, err := mentionLink(m)
		if err != nil {
			return nil, nil, err
		}
		blocks = append(blocks, link)
	}
	var atts []Attachment
	total := 0
	for _, f := range p.Attachments {
		total += len(f.Data)
		if total > MaxAttachments {
			return nil, nil, fmt.Errorf("%w: attachments are limited to %d MiB in total", ErrInvalid, MaxAttachments>>20)
		}
		name := path.Base(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." || name == "/" || name == "" {
			name = "attachment"
		}
		name = clip(name, 255)
		mt := f.MimeType
		if _, _, err := mime.ParseMediaType(mt); err != nil || mt == "" {
			mt = http.DetectContentType(f.Data)
		}
		mt, _, _ = strings.Cut(mt, ";")
		atts = append(atts, Attachment{Name: name, MimeType: mt, Size: int64(len(f.Data))})
		switch {
		case strings.HasPrefix(mt, "image/"):
			if !caps.PromptCapabilities.Image {
				return nil, nil, ErrNoImages
			}
			blocks = append(blocks, acp.ContentBlock{Type: "image", MimeType: mt, Data: base64.StdEncoding.EncodeToString(f.Data)})
		default:
			if !caps.PromptCapabilities.EmbeddedContext {
				return nil, nil, fmt.Errorf("%w: this agent can't take attached files", ErrInvalid)
			}
			res := map[string]string{"uri": "file:///attachments/" + name, "mimeType": mt}
			if utf8.Valid(f.Data) {
				res["text"] = string(f.Data)
			} else {
				res["blob"] = base64.StdEncoding.EncodeToString(f.Data)
			}
			raw, _ := json.Marshal(res)
			blocks = append(blocks, acp.ContentBlock{Type: "resource", Resource: raw})
		}
	}
	return blocks, atts, nil
}

// mentionLink is the resource_link of a mentioned file, which every ACP agent takes: a
// file:// URI in /workspace, with #L20-L28 for lines.
func mentionLink(m Mention) (acp.ContentBlock, error) {
	clean := path.Clean(m.Path)
	switch {
	case m.Path == "" || clean != m.Path || clean == "." || strings.HasPrefix(clean, "/") ||
		clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(clean, "\x00\n"):
		return acp.ContentBlock{}, fmt.Errorf("%w: %q is not a path in /workspace", ErrInvalid, m.Path)
	case m.Start < 0 || m.End < 0 || (m.End != 0 && (m.Start == 0 || m.End < m.Start)):
		return acp.ContentBlock{}, fmt.Errorf("%w: the lines of %s are not a range", ErrInvalid, m.Path)
	}
	uri := (&url.URL{Scheme: "file", Path: "/workspace/" + clean}).String()
	switch {
	case m.End > m.Start:
		uri += fmt.Sprintf("#L%d-L%d", m.Start, m.End)
	case m.Start > 0:
		uri += fmt.Sprintf("#L%d", m.Start)
	}
	return acp.ContentBlock{Type: "resource_link", URI: uri, Name: path.Base(clean)}, nil
}
