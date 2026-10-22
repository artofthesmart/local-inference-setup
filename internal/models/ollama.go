package models

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Ollama talks to the local Ollama server.
type Ollama struct {
	BaseURL string
	// HTTP has no overall timeout; each call sets its own through the context, because downloads and first model loads are slow.
	HTTP *http.Client
}

func (o Ollama) url(path string) string { return strings.TrimRight(o.BaseURL, "/") + path }

func (o Ollama) do(ctx context.Context, method, path string, body, into any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.url(path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, apiError(msg))
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func apiError(b []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != nil {
		if m, ok := e.Error.(map[string]any); ok {
			return fmt.Sprint(m["message"])
		}
		return fmt.Sprint(e.Error)
	}
	return strings.TrimSpace(string(b))
}

// Installed lists the models already downloaded, by canonical tag.
func (o Ollama) Installed(ctx context.Context) ([]string, error) {
	var r struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := o.do(ctx, http.MethodGet, "/api/tags", nil, &r); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range r.Models {
		out = append(out, Canonical(m.Name))
	}
	return out, nil
}

// Shown is what /api/show says about a downloaded model.
type Shown struct {
	Capabilities []string
	RemoteHost   string
	Context      uint64
}

// Show reads a downloaded model's details.
func (o Ollama) Show(ctx context.Context, tag string) (Shown, error) {
	var r struct {
		Capabilities []string       `json:"capabilities"`
		RemoteHost   string         `json:"remote_host"`
		ModelInfo    map[string]any `json:"model_info"`
	}
	if err := o.do(ctx, http.MethodPost, "/api/show", map[string]string{"model": tag}, &r); err != nil {
		return Shown{}, err
	}
	s := Shown{Capabilities: r.Capabilities, RemoteHost: r.RemoteHost}
	if arch, ok := r.ModelInfo["general.architecture"].(string); ok {
		if n, ok := r.ModelInfo[arch+".context_length"].(float64); ok {
			s.Context = uint64(n)
		}
	}
	return s, nil
}

// HasTools reports whether Ollama says the model supports tool calling.
func (s Shown) HasTools() bool { return slices.Contains(s.Capabilities, "tools") }

// Loaded is one model in memory, from /api/ps.
type Loaded struct {
	Tag     string
	Size    uint64
	VRAM    uint64
	Context uint64
}

// GPUShare is the percent of the model held in GPU memory.
func (l Loaded) GPUShare() int {
	if l.Size == 0 {
		return 0
	}
	return int(l.VRAM * 100 / l.Size)
}

// Running lists the models in memory.
func (o Ollama) Running(ctx context.Context) ([]Loaded, error) {
	var r struct {
		Models []struct {
			Name     string `json:"name"`
			Size     uint64 `json:"size"`
			SizeVRAM uint64 `json:"size_vram"`
			Context  uint64 `json:"context_length"`
		} `json:"models"`
	}
	if err := o.do(ctx, http.MethodGet, "/api/ps", nil, &r); err != nil {
		return nil, err
	}
	var out []Loaded
	for _, m := range r.Models {
		out = append(out, Loaded{Tag: Canonical(m.Name), Size: m.Size, VRAM: m.SizeVRAM, Context: m.Context})
	}
	return out, nil
}

// Progress is one download update.
type Progress struct {
	Status    string
	Total     uint64
	Completed uint64
}

// Pull downloads a model, reporting progress as it goes.
func (o Ollama) Pull(ctx context.Context, tag string, progress func(Progress)) error {
	b, _ := json.Marshal(map[string]any{"model": tag, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url("/api/pull"), bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download failed: %s %s", resp.Status, apiError(msg))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	done := false
	for sc.Scan() {
		var p struct {
			Status    string `json:"status"`
			Total     uint64 `json:"total"`
			Completed uint64 `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &p) != nil {
			continue
		}
		if p.Error != "" {
			return errors.New("download failed: " + p.Error)
		}
		if p.Status == "success" {
			done = true
		}
		if progress != nil {
			progress(Progress{Status: p.Status, Total: p.Total, Completed: p.Completed})
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("download interrupted: %w", err)
	}
	if !done {
		return errors.New("download ended before it finished; run again to resume")
	}
	return nil
}

// ToolTest is the result of the tool-call test.
type ToolTest struct {
	Passed bool
	// Truncated means the model ran out of answer space before finishing.
	Truncated bool
	Reason    string
	Elapsed   time.Duration
}

// toolTestRequest is Ollama's documented weather example, sent the way Copilot sends requests: streaming, through the OpenAI-compatible API.
func toolTestRequest(tag string, maxTokens int) map[string]any {
	return map[string]any{
		"model":       tag,
		"stream":      true,
		"temperature": 0,
		"max_tokens":  maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": "What is the temperature in New York?"},
		},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_temperature",
				"description": "Get the current temperature for a city",
				"parameters": map[string]any{
					"type":       "object",
					"required":   []string{"city"},
					"properties": map[string]any{"city": map[string]string{"type": "string", "description": "The name of the city"}},
				},
			},
		}},
	}
}

// TestToolCall asks the model to call a tool and checks that it sends back a real, structured tool call. Nothing is actually run.
func (o Ollama) TestToolCall(ctx context.Context, tag string, maxTokens int) (ToolTest, error) {
	start := time.Now()
	b, _ := json.Marshal(toolTestRequest(tag, maxTokens))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url("/v1/chat/completions"), bytes.NewReader(b))
	if err != nil {
		return ToolTest{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return ToolTest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return ToolTest{}, fmt.Errorf("the model didn't answer: %s %s", resp.Status, apiError(msg))
	}
	res := ParseToolStream(resp.Body)
	res.Elapsed = time.Since(start)
	return res, nil
}

// ParseToolStream reads a streamed chat answer and decides whether it holds the expected tool call.
func ParseToolStream(r io.Reader) ToolTest {
	type call struct{ name, args string }
	calls := map[int]*call{}
	var text strings.Builder
	finish := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int `json:"index"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return ToolTest{Reason: "the model returned an error: " + chunk.Error.Message}
		}
		for _, ch := range chunk.Choices {
			text.WriteString(ch.Delta.Content)
			for _, tc := range ch.Delta.ToolCalls {
				c := calls[tc.Index]
				if c == nil {
					c = &call{}
					calls[tc.Index] = c
				}
				c.name += tc.Function.Name
				c.args += tc.Function.Arguments
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finish = *ch.FinishReason
			}
		}
	}
	for _, c := range calls {
		if c.name != "get_temperature" {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(c.args), &args); err != nil {
			return ToolTest{Reason: "the tool call's arguments weren't valid JSON: " + clip(c.args)}
		}
		city, _ := args["city"].(string)
		if !strings.Contains(strings.ToLower(city), "new york") {
			return ToolTest{Reason: fmt.Sprintf("the tool call asked for %q instead of New York", city)}
		}
		return ToolTest{Passed: true}
	}
	if len(calls) > 0 {
		for _, c := range calls {
			return ToolTest{Reason: fmt.Sprintf("the model called %q instead of get_temperature", c.name)}
		}
	}
	if finish == "length" {
		return ToolTest{Truncated: true, Reason: "the model ran out of answer space before calling the tool"}
	}
	if strings.Contains(text.String(), "get_temperature") {
		return ToolTest{Reason: "the model wrote the tool call as plain text instead of making a real tool call, so Copilot can't use it"}
	}
	return ToolTest{Reason: "the model answered in words and never called the tool: " + clip(text.String())}
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:157] + "..."
	}
	if s == "" {
		return "(empty answer)"
	}
	return s
}
