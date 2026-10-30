package models

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/hardware"
)

// ggufBuilder writes small, made-up GGUF headers for tests.
type ggufBuilder struct {
	b     bytes.Buffer
	count uint64
}

func (g *ggufBuilder) str(s string) {
	binary.Write(&g.b, binary.LittleEndian, uint64(len(s)))
	g.b.WriteString(s)
}

func (g *ggufBuilder) kv(key string, typ uint32, val any) {
	g.count++
	g.str(key)
	binary.Write(&g.b, binary.LittleEndian, typ)
	switch v := val.(type) {
	case string:
		g.str(v)
	case []uint32:
		binary.Write(&g.b, binary.LittleEndian, uint32(ggufU32))
		binary.Write(&g.b, binary.LittleEndian, uint64(len(v)))
		binary.Write(&g.b, binary.LittleEndian, v)
	case []string:
		binary.Write(&g.b, binary.LittleEndian, uint32(ggufString))
		binary.Write(&g.b, binary.LittleEndian, uint64(len(v)))
		for _, s := range v {
			g.str(s)
		}
	default:
		binary.Write(&g.b, binary.LittleEndian, v)
	}
}

func (g *ggufBuilder) bytes() []byte {
	var out bytes.Buffer
	out.WriteString("GGUF")
	binary.Write(&out, binary.LittleEndian, uint32(3))
	binary.Write(&out, binary.LittleEndian, uint64(291))
	binary.Write(&out, binary.LittleEndian, g.count)
	out.Write(g.b.Bytes())
	return out.Bytes()
}

func llamaHeader(ctx uint64) []byte {
	var g ggufBuilder
	g.kv("general.architecture", ggufString, "llama")
	g.kv("general.name", ggufString, "Test 8B")
	g.kv("general.file_type", ggufU32, uint32(15))
	g.kv("llama.context_length", ggufU64, ctx)
	g.kv("llama.block_count", ggufU32, uint32(32))
	g.kv("llama.embedding_length", ggufU32, uint32(4096))
	g.kv("llama.attention.head_count", ggufU32, uint32(32))
	g.kv("llama.attention.head_count_kv", ggufArray, []uint32{8, 8, 4})
	g.kv("llama.rope.freq_base", ggufF32, float32(500000))
	g.kv("llama.use_parallel", ggufBool, uint8(1))
	g.kv("tokenizer.ggml.tokens", ggufArray, []string{"a", "b", "c"})
	return g.bytes()
}

func TestParseGGUF(t *testing.T) {
	h, err := ParseGGUF(llamaHeader(131072))
	if err != nil {
		t.Fatal(err)
	}
	if h.Arch != "llama" || h.ContextLength() != 131072 || h.Truncated {
		t.Fatalf("%+v", h)
	}
	// 32 layers x 8 KV heads x (128 + 128) x 2 bytes
	if got := h.kvBytesPerToken(); got != 32*8*256*2 {
		t.Errorf("kv: %d", got)
	}
}

func TestKVHybridAndMLA(t *testing.T) {
	parse := func(g *ggufBuilder) Header {
		t.Helper()
		h, err := ParseGGUF(g.bytes())
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := func(arch string, layers uint32) *ggufBuilder {
		g := &ggufBuilder{}
		g.kv("general.architecture", ggufString, arch)
		g.kv(arch+".block_count", ggufU32, layers)
		g.kv(arch+".attention.head_count", ggufU32, uint32(32))
		g.kv(arch+".attention.key_length", ggufU32, uint32(128))
		g.kv(arch+".attention.value_length", ggufU32, uint32(128))
		return g
	}
	per := uint64(8 * 256 * 2) // one layer: 8 KV heads x (128 + 128) x 2 bytes

	// Per-layer list with 0 for state-space layers: only 2 of 6 layers keep a cache.
	g := base("nemotron_h", 6)
	g.kv("nemotron_h.attention.head_count_kv", ggufArray, []uint32{0, 0, 8, 0, 0, 8})
	if got := parse(g).kvBytesPerToken(); got != 2*per {
		t.Errorf("per-layer list: %d", got)
	}

	// Full attention every 4th layer: 48 layers -> 12.
	g = base("qwen35", 48)
	g.kv("qwen35.attention.head_count_kv", ggufU32, uint32(8))
	g.kv("qwen35.full_attention_interval", ggufU32, uint32(4))
	if got := parse(g).kvBytesPerToken(); got != 12*per {
		t.Errorf("interval: %d", got)
	}

	// State-space model with no layout: assume one cache layer in four (rounded up).
	g = base("qwen3next", 50)
	g.kv("qwen3next.attention.head_count_kv", ggufU32, uint32(8))
	g.kv("qwen3next.ssm.inner_size", ggufU32, uint32(4096))
	if got := parse(g).kvBytesPerToken(); got != 13*per {
		t.Errorf("ssm: %d", got)
	}

	// Compressed (MLA) cache: one 512+64 key and 512 value vector per layer.
	g = base("deepseek2", 10)
	g.kv("deepseek2.attention.kv_lora_rank", ggufU32, uint32(512))
	g.kv("deepseek2.attention.key_length_mla", ggufU32, uint32(576))
	g.kv("deepseek2.attention.value_length_mla", ggufU32, uint32(512))
	if got := parse(g).kvBytesPerToken(); got != 10*(576+512)*2 {
		t.Errorf("mla: %d", got)
	}
	g = base("deepseek2", 10)
	g.kv("deepseek2.attention.kv_lora_rank", ggufU32, uint32(512))
	if got := parse(g).kvBytesPerToken(); got != 10*576*2 {
		t.Errorf("mla without lengths: %d", got)
	}
}

func TestParseGGUF32BitContext(t *testing.T) {
	var g ggufBuilder
	g.kv("general.architecture", ggufString, "qwen3")
	g.kv("qwen3.context_length", ggufU32, uint32(40960))
	h, err := ParseGGUF(g.bytes())
	if err != nil || h.ContextLength() != 40960 {
		t.Fatalf("%v %+v", err, h)
	}
	if h.kvBytesPerToken() != 0 {
		t.Error("missing layout should give 0")
	}
}

func TestParseGGUFTruncatedKeepsEarlySettings(t *testing.T) {
	b := llamaHeader(65536)
	h, err := ParseGGUF(b[:len(b)-5])
	if err != nil || !h.Truncated || h.ContextLength() != 65536 {
		t.Fatalf("%v %+v", err, h)
	}
}

func TestParseGGUFBad(t *testing.T) {
	if _, err := ParseGGUF([]byte("NOPE1234")); err == nil {
		t.Error("bad magic")
	}
	var g ggufBuilder
	g.kv("llama.context_length", ggufU64, uint64(1))
	if _, err := ParseGGUF(g.bytes()); err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Errorf("no arch: %v", err)
	}
	old := llamaHeader(1)
	binary.LittleEndian.PutUint32(old[4:], 1)
	if _, err := ParseGGUF(old); err == nil {
		t.Error("v1 should be refused")
	}
	var bad ggufBuilder
	bad.kv("general.architecture", ggufString, "x")
	bad.kv("weird", 99, uint32(0))
	if _, err := ParseGGUF(bad.bytes()); err == nil {
		t.Error("unknown type should fail")
	}
}

func TestValidTag(t *testing.T) {
	good := []string{"gpt-oss:20b", "qwen3.6", "library/llama3.1:8b", "user/model:q4_k_m"}
	bad := []string{"", "https://ollama.com/library/x", "hf.co/foo/bar", "gpt-oss:120b-cloud", "ollama pull x", "x; rm -rf /", "a/b/c", "a:b:c", "Ünïcode"}
	for _, s := range good {
		if err := ValidTag(Canonical(s)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidTag(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
	if Canonical("Qwen3.6") != "qwen3.6:latest" || Canonical("a/b") != "a/b:latest" || Canonical("x:1") != "x:1" {
		t.Error("canonical")
	}
}

func TestParseShortlist(t *testing.T) {
	list, err := ParseShortlist("# c\n\nllama3.1:8b  Small one\nqwen3.6 Big\nllama3.1:8b dup\n")
	if err != nil || len(list) != 2 || list[1].Tag != "qwen3.6:latest" || list[0].Note != "Small one" {
		t.Fatalf("%v %+v", err, list)
	}
	if _, err := ParseShortlist("# only comments\n"); err == nil {
		t.Error("empty list")
	}
	if _, err := ParseShortlist("foo-cloud\n"); err == nil {
		t.Error("cloud model in list")
	}
	list, err = ParseShortlist("a:1 A\n[Menu-Only]\n# big ones\nb:2 B\n")
	if err != nil || len(list) != 2 || list[0].MenuOnly || !list[1].MenuOnly || list[1].Note != "B" {
		t.Fatalf("menu-only: %v %+v", err, list)
	}
	builtin, src, err := LoadShortlist("/does/not/exist")
	if err != nil || src != "built-in list" || len(builtin) < 10 {
		t.Fatalf("%v %s %d", err, src, len(builtin))
	}
	auto := 0
	for _, c := range builtin {
		if !c.MenuOnly {
			auto++
		}
	}
	if auto < 5 || auto == len(builtin) {
		t.Errorf("built-in list should have auto-pick and menu-only models: %d of %d", auto, len(builtin))
	}
}

// fakeRegistry serves manifests and model-file headers.
func fakeRegistry(t *testing.T, models map[string][]byte, sizes map[string]uint64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, header := range models {
			path, tag, _ := strings.Cut(name, ":")
			switch r.URL.Path {
			case "/v2/library/" + path + "/manifests/" + tag:
				if r.Header.Get("Accept") == "" {
					t.Error("no Accept header")
				}
				json.NewEncoder(w).Encode(map[string]any{"layers": []map[string]any{
					{"mediaType": "application/vnd.ollama.image.model", "digest": "sha256:" + path, "size": sizes[name]},
					{"mediaType": "application/vnd.ollama.image.template", "digest": "sha256:t", "size": 100},
				}})
				return
			case "/v2/library/" + path + "/blobs/sha256:" + path:
				if r.Header.Get("Range") != "bytes=0-1048575" {
					t.Errorf("range: %q", r.Header.Get("Range"))
				}
				w.WriteHeader(http.StatusPartialContent)
				w.Write(header)
				return
			}
		}
		http.NotFound(w, r)
	}))
}

func TestCheckShortlist(t *testing.T) {
	srv := fakeRegistry(t, map[string][]byte{
		"big:latest":  llamaHeader(131072),
		"small:1b":    llamaHeader(131072),
		"shortctx:7b": llamaHeader(32768),
		"notgguf:7b":  []byte("garbage!"),
	}, map[string]uint64{"big:latest": 20 * gib, "small:1b": 2 * gib, "shortctx:7b": gib, "notgguf:7b": gib})
	defer srv.Close()
	r := Registry{BaseURL: srv.URL, HTTP: srv.Client()}
	list := []Candidate{{Tag: "big:latest", Note: "B", MenuOnly: true}, {Tag: "small:1b"}, {Tag: "shortctx:7b"}, {Tag: "notgguf:7b"}, {Tag: "missing:1b"}}
	got := r.CheckShortlist(t.Context(), list, 65536)
	if len(got.OK) != 2 || got.OK[0].Tag != "small:1b" || got.OK[1].Note != "B" || got.OK[1].Size != 20*gib+100 || !got.OK[1].MenuOnly || got.OK[1].Rank != 0 || got.OK[0].Rank != 1 {
		t.Fatalf("%+v", got.OK)
	}
	joined := strings.Join(got.Skipped, "\n")
	for _, want := range []string{"shortctx:7b: supports only 32768", "notgguf:7b", "missing:1b isn't in Ollama's registry"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestPick(t *testing.T) {
	kv := uint64(32 * 8 * 256 * 2) // 8 GiB at 64k
	small := Info{Tag: "small", Size: 4 * gib, KVPerToken: kv}
	mid := Info{Tag: "mid", Size: 12 * gib, KVPerToken: kv / 4}
	big := Info{Tag: "big", Size: 20 * gib, KVPerToken: kv / 4}
	list := []Info{small, mid, big}
	cases := []struct {
		name   string
		budget uint64
		want   string
		fits   bool
	}{
		{"24 GB GPU", 24 * gib * 9 / 10, "mid", true},
		{"48 GB Mac", 36 * gib, "big", true},
		{"16 GB Mac", 16 * gib * 2 / 3, "small", false},
		{"8 GB laptop CPU", 4 * gib, "small", false},
		{"no budget", 0, "small", false},
	}
	for _, c := range cases {
		got, fits := Pick(list, c.budget, 65536)
		if got.Tag != c.want || fits != c.fits {
			t.Errorf("%s: got %s fits=%v", c.name, got.Tag, fits)
		}
	}
	// List order beats size, and menu-only models are never auto-picked.
	ranked := []Info{
		{Tag: "huge-dense", Size: 30 * gib, KVPerToken: kv / 4, Rank: 3, MenuOnly: true},
		{Tag: "bigger-older", Size: 20 * gib, KVPerToken: kv / 4, Rank: 0},
		{Tag: "smaller-better", Size: 18 * gib, KVPerToken: kv / 4, Rank: 1},
	}
	if got, fits := Pick(ranked, 100*gib, 65536); got.Tag != "smaller-better" || !fits {
		t.Errorf("rank: got %s", got.Tag)
	}
	if got, fits := Pick(ranked[:1], 100*gib, 65536); got.Tag != "huge-dense" || !fits {
		t.Errorf("only menu-only models: got %s", got.Tag)
	}
	if _, fits := Pick(nil, gib, 1); fits {
		t.Error("empty list")
	}
	if need := (Info{Size: gib}).MemoryNeed(65536); need != 10*gib {
		t.Errorf("unknown layout estimate: %d", need/gib)
	}
}

func fakeOllama(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) Ollama {
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return Ollama{BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestOllamaAPI(t *testing.T) {
	o := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"gpt-oss:20b"},{"name":"qwen3.6"}]}`)
		case "/api/show":
			fmt.Fprint(w, `{"capabilities":["completion","tools"],"model_info":{"general.architecture":"gptoss","gptoss.context_length":131072}}`)
		case "/api/ps":
			fmt.Fprint(w, `{"models":[{"name":"gpt-oss:20b","size":20000,"size_vram":15000,"context_length":65536}]}`)
		case "/api/pull":
			fmt.Fprintln(w, `{"status":"pulling manifest"}`)
			fmt.Fprintln(w, `{"status":"pulling abc","total":100,"completed":50}`)
			fmt.Fprintln(w, `{"status":"success"}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":"model 'x' not found"}`)
		}
	})
	ctx := t.Context()
	tags, err := o.Installed(ctx)
	if err != nil || len(tags) != 2 || tags[1] != "qwen3.6:latest" {
		t.Fatalf("%v %v", err, tags)
	}
	s, err := o.Show(ctx, "gpt-oss:20b")
	if err != nil || !s.HasTools() || s.Context != 131072 || s.RemoteHost != "" {
		t.Fatalf("%v %+v", err, s)
	}
	ps, err := o.Running(ctx)
	if err != nil || len(ps) != 1 || ps[0].Context != 65536 || ps[0].GPUShare() != 75 {
		t.Fatalf("%v %+v", err, ps)
	}
	var seen []Progress
	if err := o.Pull(ctx, "gpt-oss:20b", func(p Progress) { seen = append(seen, p) }); err != nil || len(seen) != 3 || seen[1].Completed != 50 {
		t.Fatalf("%v %+v", err, seen)
	}
	if err := o.do(ctx, "GET", "/nope", nil, nil); err == nil || !strings.Contains(err.Error(), "model 'x' not found") {
		t.Errorf("error message: %v", err)
	}
}

func TestPullErrors(t *testing.T) {
	o := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"error":"no space left on device"}`)
	})
	if err := o.Pull(t.Context(), "x", nil); err == nil || !strings.Contains(err.Error(), "no space") {
		t.Errorf("%v", err)
	}
	cut := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"status":"pulling"}`) })
	if err := cut.Pull(t.Context(), "x", nil); err == nil || !strings.Contains(err.Error(), "resume") {
		t.Errorf("%v", err)
	}
}

func sse(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: " + c + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestParseToolStream(t *testing.T) {
	cases := []struct {
		name      string
		stream    string
		pass      bool
		truncated bool
		reason    string
	}{
		{"real call, in pieces", sse(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_temp"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"erature","arguments":"{\"city\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"New York\"}"}}]},"finish_reason":"tool_calls"}]}`,
		), true, false, ""},
		{"call written as text", sse(`{"choices":[{"delta":{"content":"{\"name\":\"get_temperature\",\"arguments\":{\"city\":\"New York\"}}"},"finish_reason":"stop"}]}`), false, false, "plain text"},
		{"ran out of space", sse(`{"choices":[{"delta":{"content":"Let me think..."},"finish_reason":"length"}]}`), false, true, "answer space"},
		{"wrong city", sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_temperature","arguments":"{\"city\":\"Boston\"}"}}]}}]}`), false, false, "Boston"},
		{"wrong tool", sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"search","arguments":"{}"}}]}}]}`), false, false, "search"},
		{"bad arguments", sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_temperature","arguments":"{city"}}]}}]}`), false, false, "valid JSON"},
		{"just words", sse(`{"choices":[{"delta":{"content":"It's sunny."},"finish_reason":"stop"}]}`), false, false, "It's sunny."},
		{"error", sse(`{"error":{"message":"model requires more system memory"}}`), false, false, "more system memory"},
	}
	for _, c := range cases {
		got := ParseToolStream(strings.NewReader(c.stream))
		if got.Passed != c.pass || got.Truncated != c.truncated || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestToolCallRequest(t *testing.T) {
	o := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/v1/chat/completions" || req["stream"] != true || req["max_tokens"] != float64(2048) || req["model"] != "m:1" {
			t.Errorf("request: %s %v", r.URL.Path, req)
		}
		fmt.Fprint(w, sse(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_temperature","arguments":"{\"city\":\"new york city\"}"}}]}}]}`))
	})
	res, err := o.TestToolCall(t.Context(), "m:1", 2048)
	if err != nil || !res.Passed {
		t.Fatalf("%v %+v", err, res)
	}
	fail := fakeOllama(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"model requires more system memory (20 GiB) than is available (8 GiB)"}`)
	})
	if _, err := fail.TestToolCall(t.Context(), "m:1", 1024); err == nil || !strings.Contains(err.Error(), "more system memory") {
		t.Errorf("%v", err)
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir)
	if err != nil || len(s.Models) != 0 {
		t.Fatal(err)
	}
	now := time.Now()
	s.Add(Qualified{Tag: "a:1", Tested: now.Add(-time.Hour)})
	s.Add(Qualified{Tag: "b:1", Tested: now})
	s.Add(Qualified{Tag: "c:1", Tested: now.Add(-2 * time.Hour)})
	s.LastUsed = "c:1"
	if o := s.Ordered(); o[0].Tag != "c:1" || o[1].Tag != "b:1" || o[2].Tag != "a:1" {
		t.Fatalf("order: %+v", o)
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := LoadState(dir)
	if err != nil || len(back.Models) != 3 || back.LastUsed != "c:1" {
		t.Fatalf("%v %+v", err, back)
	}
	if !back.Prune([]string{"a:1", "b:1"}) || back.LastUsed != "" || len(back.Models) != 2 {
		t.Fatalf("prune: %+v", back)
	}
	if back.Prune([]string{"a:1", "b:1"}) {
		t.Error("nothing to prune")
	}
	if _, ok := back.Get("a:1"); !ok {
		t.Error("get")
	}
}

func TestDamagedState(t *testing.T) {
	dir := t.TempDir()
	(State{}).Save(dir)
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(dir); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("%v", err)
	}
}

var allowed = []string{"llama3.1:8b", "gpt-oss:20b", "qwen3.6:latest"}

func TestParseAnswer(t *testing.T) {
	good := map[string]string{
		"MODEL: gpt-oss:20b\n\nHere's why...": "gpt-oss:20b",
		"**MODEL:** `gpt-oss:20b`":            "gpt-oss:20b",
		"MODEL: gpt-oss:20b\nSPEED: llama3.1:8b\nBALANCED: gpt-oss:20b\nINTELLIGENCE: qwen3.6:latest": "gpt-oss:20b",
		"  gpt-oss:20b  ":                       "gpt-oss:20b",
		"qwen3.6":                               "qwen3.6:latest",
		"Sure!\nmodel: llama3.1:8b\nMore text.": "llama3.1:8b",
	}
	for in, want := range good {
		if got, err := ParseAnswer(in, allowed); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := ParseAnswer("MODEL: NONE\nYour laptop is too small.", allowed); !errors.Is(err, ErrNone) {
		t.Errorf("none: %v", err)
	}
	bad := map[string]string{
		"MODEL: gpt-oss:120b":                       "isn't on the checked list",
		"MODEL: gpt-oss:120b-cloud":                 "cloud",
		"MODEL: https://ollama.com/library/gpt-oss": "link",
		"ollama pull gpt-oss:20b":                   "command",
		"MODEL: hf.co/unsloth/foo":                  "Hugging Face",
	}
	for in, want := range bad {
		if _, err := ParseAnswer(in, allowed); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestPromptPerOS(t *testing.T) {
	cands := []Info{{Tag: "gpt-oss:20b", Size: 13 * gib, Context: 131072}}
	win := hardware.Hardware{OS: "windows", CPU: "Intel Ultra 7", RAM: 32 * gib, VisibleRAM: 31 * gib, GPUs: []hardware.GPU{{Name: "Intel Arc", Vendor: "intel", Integrated: true}}, FreeDisk: 100 * gib}
	p := Prompt(win, cands, 65536)
	for _, want := range []string{"OS: Windows", "Installed physical RAM: 32.0 GB", "Windows-visible physical RAM: 31.0 GB", "iGPU planning ceiling: up to 50% of installed RAM (16.0 GB)", "- gpt-oss:20b | DOWNLOAD 13.0 GB | VERIFIED_CONTEXT_TOKENS 131072", "MODEL: NONE", "at least 65536 tokens", "GitHub Copilot agent mode", "Free space where models are saved: 100.0 GB", "1. SPEED:", "2. BALANCED:", "3. INTELLIGENCE:", "Use the BALANCED choice in this copy-back line", "Say NONE for any tier without a practical fit"} {
		if !strings.Contains(p, want) {
			t.Errorf("windows prompt missing %q\n%s", want, p)
		}
	}
	mac := Prompt(hardware.Hardware{OS: "darwin", AppleSilicon: true, RAM: 32 * gib, GPUs: []hardware.GPU{{Name: "Apple GPU", Integrated: true, Vendor: "apple"}}}, cands, 65536)
	if !strings.Contains(mac, "unified memory") || strings.Contains(mac, "iGPU planning") || !strings.Contains(mac, "RAM: 32.0 GB") || !strings.Contains(mac, "unknown") {
		t.Errorf("mac prompt:\n%s", mac)
	}
	intel := Prompt(hardware.Hardware{OS: "darwin", IntelMac: true, RAM: 16 * gib}, cands, 65536)
	if !strings.Contains(intel, "CPU only") || !strings.Contains(intel, "GPU: none found") {
		t.Errorf("intel mac prompt:\n%s", intel)
	}
	linux := Prompt(hardware.Hardware{OS: "linux", RAM: 64 * gib, GPUs: []hardware.GPU{{Name: "RTX 4090", Vendor: "nvidia", VRAM: 24 * gib}}}, cands, 32768)
	if !strings.Contains(linux, "RTX 4090 (24.0 GB dedicated video memory)") || !strings.Contains(linux, "at least 32768 tokens") {
		t.Errorf("linux prompt:\n%s", linux)
	}
}
