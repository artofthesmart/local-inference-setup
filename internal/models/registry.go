package models

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
)

// DefaultRegistry is Ollama's public model registry.
const DefaultRegistry = "https://registry.ollama.ai"

//go:embed shortlist.txt
var builtinShortlist string

// Candidate is one shortlist entry.
type Candidate struct {
	Tag  string
	Note string
	// MenuOnly models are offered in the menu but never picked automatically.
	MenuOnly bool
}

// menuOnlyHeading starts the shortlist section of models that are never picked automatically.
const menuOnlyHeading = "[menu-only]"

// LoadShortlist reads the user's copy of the shortlist if there is one, otherwise the built-in list.
func LoadShortlist(overridePath string) ([]Candidate, string, error) {
	text, source := builtinShortlist, "built-in list"
	if overridePath != "" {
		if b, err := os.ReadFile(overridePath); err == nil {
			text, source = string(b), overridePath
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", err
		}
	}
	list, err := ParseShortlist(text)
	return list, source, err
}

// ParseShortlist reads "tag  description" lines, skipping blanks and # comments. Lines after a "[menu-only]" line are never picked automatically.
func ParseShortlist(text string) ([]Candidate, error) {
	var out []Candidate
	seen := map[string]bool{}
	menuOnly := false
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.EqualFold(line, menuOnlyHeading) {
			menuOnly = true
			continue
		}
		f := strings.Fields(line)
		tag := Canonical(f[0])
		if err := ValidTag(tag); err != nil {
			return nil, fmt.Errorf("shortlist line %d: %w", i+1, err)
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, Candidate{Tag: tag, Note: strings.Join(f[1:], " "), MenuOnly: menuOnly})
	}
	if len(out) == 0 {
		return nil, errors.New("the shortlist is empty")
	}
	return out, nil
}

// Info is what the registry says about one model.
type Info struct {
	Tag     string
	Note    string
	Size    uint64 // download size in bytes
	Context uint64 // trained context window in tokens
	// KVPerToken is the estimated context-cache memory per token; 0 if unknown.
	KVPerToken uint64
	Arch       string
	// Rank is the model's place in the shortlist; later lines are preferred when picking automatically.
	Rank     int
	MenuOnly bool
}

// Registry reads model facts from Ollama's registry without downloading the model.
type Registry struct {
	BaseURL string
	HTTP    *http.Client
}

const headerBytes = 1 << 20

// Inspect reads a model's download size and the settings at the start of its model file.
func (r Registry) Inspect(ctx context.Context, tag string) (Info, error) {
	ref, err := parseTag(tag)
	if err != nil {
		return Info{}, err
	}
	base := strings.TrimRight(r.BaseURL, "/") + "/v2/" + ref.Namespace + "/" + ref.Name
	var man struct {
		Config struct {
			Size uint64 `json:"size"`
		} `json:"config"`
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      uint64 `json:"size"`
		} `json:"layers"`
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/manifests/"+ref.Tag, nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("couldn't reach Ollama's registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Info{}, fmt.Errorf("%s isn't in Ollama's registry", ref)
	}
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("registry: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&man); err != nil {
		return Info{}, fmt.Errorf("registry: %w", err)
	}
	info := Info{Tag: ref.String()}
	digest := ""
	for _, l := range man.Layers {
		info.Size += l.Size
		if l.MediaType == "application/vnd.ollama.image.model" {
			digest = l.Digest
		}
	}
	if digest == "" {
		return info, fmt.Errorf("%s has no model file in the registry", ref)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, base+"/blobs/"+digest, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", headerBytes-1))
	blob, err := r.HTTP.Do(req)
	if err != nil {
		return info, fmt.Errorf("couldn't read %s's model file: %w", ref, err)
	}
	defer blob.Body.Close()
	if blob.StatusCode != http.StatusPartialContent && blob.StatusCode != http.StatusOK {
		return info, fmt.Errorf("couldn't read %s's model file: %s", ref, blob.Status)
	}
	// Read at most the first MiB, even if the server ignored the range and sent everything.
	b, err := io.ReadAll(io.LimitReader(blob.Body, headerBytes))
	if err != nil {
		return info, err
	}
	h, err := ParseGGUF(b)
	if err != nil {
		return info, fmt.Errorf("%s: %w", ref, err)
	}
	info.Arch, info.Context, info.KVPerToken = h.Arch, h.ContextLength(), h.kvBytesPerToken()
	if info.Context == 0 {
		return info, fmt.Errorf("%s doesn't say how much context it supports", ref)
	}
	return info, nil
}

// Checked is the result of checking the shortlist.
type Checked struct {
	OK      []Info
	Skipped []string // plain-English reasons, one per skipped model
}

// CheckShortlist inspects every candidate at once and keeps those that support the wanted context.
func (r Registry) CheckShortlist(ctx context.Context, list []Candidate, wantContext uint64) Checked {
	type result struct {
		i    int
		info Info
		err  error
	}
	results := make([]result, len(list))
	var wg sync.WaitGroup
	for i, c := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := r.Inspect(ctx, c.Tag)
			info.Note, info.Rank, info.MenuOnly = c.Note, i, c.MenuOnly
			results[i] = result{i, info, err}
		}()
	}
	wg.Wait()
	var out Checked
	for _, res := range results {
		switch {
		case res.err != nil:
			out.Skipped = append(out.Skipped, fmt.Sprintf("%s: %v", list[res.i].Tag, res.err))
		case res.info.Context < wantContext:
			out.Skipped = append(out.Skipped, fmt.Sprintf("%s: supports only %d tokens of context", res.info.Tag, res.info.Context))
		default:
			out.OK = append(out.OK, res.info)
		}
	}
	sort.SliceStable(out.OK, func(a, b int) bool { return out.OK[a].Size < out.OK[b].Size })
	return out
}

const (
	gib = 1 << 30
	// overhead covers Ollama's working memory beyond the weights and cache.
	overhead = 1 * gib
)

// MemoryNeed estimates the memory a model needs at a context length: weights, context cache, and working memory.
func (i Info) MemoryNeed(contextLen uint64) uint64 {
	kv := i.KVPerToken * contextLen
	if i.KVPerToken == 0 {
		// Unknown layout: assume a typical 8B model's cache, scaled to the context.
		kv = 8 * gib * contextLen / 65536
	}
	return i.Size + kv + overhead
}

// Fits reports whether a model fits in a memory budget at a context length.
func (i Info) Fits(budget, contextLen uint64) bool {
	return budget > 0 && i.MemoryNeed(contextLen) <= budget
}

// Pick returns the preferred model that fits: the latest in the shortlist, then the biggest. Menu-only models are skipped. When none fit, it returns the smallest with fits=false.
func Pick(list []Info, budget, contextLen uint64) (best Info, fits bool) {
	auto := slices.DeleteFunc(slices.Clone(list), func(m Info) bool { return m.MenuOnly })
	if len(auto) == 0 {
		auto = list
	}
	if len(auto) == 0 {
		return Info{}, false
	}
	smallest := auto[0]
	for _, m := range auto {
		if m.MemoryNeed(contextLen) < smallest.MemoryNeed(contextLen) {
			smallest = m
		}
		if m.Fits(budget, contextLen) && (!fits || m.Rank > best.Rank || (m.Rank == best.Rank && m.Size > best.Size)) {
			best, fits = m, true
		}
	}
	if !fits {
		return smallest, false
	}
	return best, true
}
