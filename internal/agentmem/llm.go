package agentmem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/personas"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LLM is a provider's utility model as extraction uses it.
type LLM interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

// Request is one completion.
type Request struct {
	System    string
	User      string
	Schema    map[string]any // the JSON schema of the answer; used where the API enforces it
	MaxTokens int
}

// Response is a completion's text and token usage.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// UtilityModel is the small model extraction calls, with its price in USD per million
// tokens when known.
type UtilityModel struct {
	Provider     string  `json:"provider,omitempty" doc:"The provider's name"`
	Kind         string  `json:"kind"`
	Model        string  `json:"model"`
	PriceInPerM  float64 `json:"priceInPerM,omitempty"`
	PriceOutPerM float64 `json:"priceOutPerM,omitempty"`
	Priced       bool    `json:"priced"`
	Note         string  `json:"note,omitempty"`
}

// cost is the price of a call in USD × 10^6.
func (m UtilityModel) cost(in, out int) int64 {
	return int64(float64(in)*m.PriceInPerM + float64(out)*m.PriceOutPerM)
}

// ErrNoUtility means the provider has no utility model Studio can call.
var ErrNoUtility = errors.New("no utility model for this provider")

// UtilityFor is the utility model of a provider kind; ok is false for subscriptions.
func UtilityFor(p store.Provider) (UtilityModel, bool) {
	switch p.Kind {
	case personas.KindAnthropicAPI:
		return UtilityModel{Kind: p.Kind, Model: "claude-haiku-4-5", PriceInPerM: 1, PriceOutPerM: 5, Priced: true}, true
	case personas.KindOpenAIAPI:
		// TODO verify (S7): gpt-6-luna is OpenAI's small model per third-party listings;
		// confirm the ID and price against the API before relying on it.
		return UtilityModel{Kind: p.Kind, Model: "gpt-6-luna", PriceInPerM: 0.10, PriceOutPerM: 0.50, Priced: true}, true
	case personas.KindOpenAICompatible:
		return UtilityModel{Kind: p.Kind, Model: p.Model, Note: "the endpoint's single model; price unknown, so calls are capped instead"}, p.Model != "" && p.BaseURL != ""
	default:
		// TODO (S8): subscriptions can't be called from the host; extraction would have to
		// run in the guest's harness.
		return UtilityModel{Kind: p.Kind, Note: "subscriptions have no extraction yet"}, false
	}
}

// DefaultUtility returns a client for the provider's utility model, calling the provider
// directly from the host with the real key.
func DefaultUtility(p store.Provider, key string) (LLM, UtilityModel, error) {
	m, ok := UtilityFor(p)
	if !ok {
		return nil, m, ErrNoUtility
	}
	hc := &http.Client{Timeout: 90 * time.Second}
	switch p.Kind {
	case personas.KindAnthropicAPI:
		return &anthropicLLM{httpc: hc, url: "https://api.anthropic.com/v1/messages", key: key, model: m.Model}, m, nil
	case personas.KindOpenAIAPI:
		return &openAILLM{httpc: hc, url: "https://api.openai.com/v1/responses", key: key, model: m.Model}, m, nil
	default:
		return &chatLLM{httpc: hc, url: strings.TrimRight(p.BaseURL, "/") + "/chat/completions", key: key, model: m.Model}, m, nil
	}
}

// post sends a JSON request and decodes the answer. The key never appears in an error:
// bodies are cut short and scrubbed.
func post(ctx context.Context, hc *http.Client, url, key string, header http.Header, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New(scrub(err.Error(), key))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return errors.New(scrub(err.Error(), key))
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", resp.Status, scrub(truncate(string(data), 300), key))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding the answer: %w", err)
	}
	return nil
}

func scrub(s, key string) string {
	if key != "" {
		s = strings.ReplaceAll(s, key, "[key]")
	}
	return s
}

type anthropicLLM struct {
	httpc      *http.Client
	url        string
	key, model string
}

func (l *anthropicLLM) Complete(ctx context.Context, r Request) (Response, error) {
	h := http.Header{}
	h.Set("x-api-key", l.key)
	h.Set("anthropic-version", "2023-06-01")
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	// The schema is in the prompt and enforced by validation; Messages API structured
	// outputs are not relied on.
	err := post(ctx, l.httpc, l.url, l.key, h, map[string]any{
		"model": l.model, "max_tokens": r.MaxTokens, "system": r.System,
		"messages": []map[string]any{{"role": "user", "content": r.User}},
	}, &out)
	res := Response{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}
	for _, c := range out.Content {
		if c.Type == "text" {
			res.Text += c.Text
		}
	}
	return res, err
}

type openAILLM struct {
	httpc      *http.Client
	url        string
	key, model string
}

func (l *openAILLM) Complete(ctx context.Context, r Request) (Response, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+l.key)
	var out struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	body := map[string]any{
		"model": l.model, "instructions": r.System, "input": r.User, "max_output_tokens": r.MaxTokens, "store": false,
	}
	if r.Schema != nil {
		body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "facts", "schema": r.Schema, "strict": true}}
	}
	err := post(ctx, l.httpc, l.url, l.key, h, body, &out)
	res := Response{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}
	for _, o := range out.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" {
				res.Text += c.Text
			}
		}
	}
	return res, err
}

// chatLLM is an OpenAI-compatible Chat Completions endpoint.
type chatLLM struct {
	httpc      *http.Client
	url        string
	key, model string
}

func (l *chatLLM) Complete(ctx context.Context, r Request) (Response, error) {
	h := http.Header{}
	if l.key != "" {
		h.Set("Authorization", "Bearer "+l.key)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	err := post(ctx, l.httpc, l.url, l.key, h, map[string]any{
		"model": l.model, "max_tokens": r.MaxTokens,
		"messages": []map[string]any{{"role": "system", "content": r.System}, {"role": "user", "content": r.User}},
	}, &out)
	res := Response{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}
	if len(out.Choices) > 0 {
		res.Text = out.Choices[0].Message.Content
	}
	return res, err
}
