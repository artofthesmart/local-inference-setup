package models

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// GGUF value types, from the GGUF spec.
const (
	ggufU8 = iota
	ggufI8
	ggufU16
	ggufI16
	ggufU32
	ggufI32
	ggufF32
	ggufBool
	ggufString
	ggufArray
	ggufU64
	ggufI64
	ggufF64
)

// Header is what the start of a GGUF model file says about the model.
type Header struct {
	Arch string
	// Numbers holds every whole-number setting that was read. Per-layer arrays keep their largest value.
	Numbers map[string]uint64
	// Truncated is true when the bytes ran out before every setting was read. Model files list tokenizer data last, so the important settings usually come first.
	Truncated bool
}

// ContextLength is the model's trained context window, or 0 if unknown.
func (h Header) ContextLength() uint64 { return h.Numbers[h.Arch+".context_length"] }

var errShort = errors.New("short read")

type reader struct {
	b   []byte
	off int
}

func (r *reader) take(n uint64) ([]byte, error) {
	if n > uint64(len(r.b)-r.off) {
		return nil, errShort
	}
	s := r.b[r.off : r.off+int(n)]
	r.off += int(n)
	return s, nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *reader) u64() (uint64, error) {
	b, err := r.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (r *reader) str() (string, error) {
	n, err := r.u64()
	if err != nil {
		return "", err
	}
	b, err := r.take(n)
	return string(b), err
}

// ParseGGUF reads the settings at the start of a GGUF file. It reads key by key rather than searching for bytes, so a number that happens to look like a key can't fool it.
func ParseGGUF(b []byte) (Header, error) {
	h := Header{Numbers: map[string]uint64{}}
	r := &reader{b: b}
	magic, err := r.take(4)
	if err != nil || !bytes.Equal(magic, []byte("GGUF")) {
		return h, errors.New("not a GGUF model file")
	}
	version, err := r.u32()
	if err != nil {
		return h, err
	}
	if version < 2 || version > 3 {
		return h, fmt.Errorf("unsupported GGUF version %d", version)
	}
	if _, err := r.u64(); err != nil { // tensor count
		return h, err
	}
	count, err := r.u64()
	if err != nil {
		return h, err
	}
	for i := uint64(0); i < count; i++ {
		key, err := r.str()
		if err != nil {
			h.Truncated = true
			break
		}
		typ, err := r.u32()
		if err != nil {
			h.Truncated = true
			break
		}
		if key == "general.architecture" && typ == ggufString {
			if h.Arch, err = r.str(); err != nil {
				h.Truncated = true
				break
			}
			continue
		}
		n, isNum, err := r.value(typ)
		if err != nil {
			if errors.Is(err, errShort) {
				h.Truncated = true
				break
			}
			return h, err
		}
		if isNum {
			h.Numbers[key] = n
		}
	}
	if h.Arch == "" {
		return h, errors.New("the model file doesn't name its architecture")
	}
	return h, nil
}

// value reads one value. Whole numbers (and arrays of them, by their largest item) are returned; everything else is skipped.
func (r *reader) value(typ uint32) (uint64, bool, error) {
	if typ == ggufArray {
		itemType, err := r.u32()
		if err != nil {
			return 0, false, err
		}
		n, err := r.u64()
		if err != nil {
			return 0, false, err
		}
		var max uint64
		isNum := false
		for j := uint64(0); j < n; j++ {
			v, num, err := r.value(itemType)
			if err != nil {
				return 0, false, err
			}
			if num {
				isNum = true
				if v > max {
					max = v
				}
			}
		}
		return max, isNum, nil
	}
	size := map[uint32]uint64{ggufU8: 1, ggufI8: 1, ggufBool: 1, ggufU16: 2, ggufI16: 2, ggufU32: 4, ggufI32: 4, ggufF32: 4, ggufU64: 8, ggufI64: 8, ggufF64: 8}
	if typ == ggufString {
		_, err := r.str()
		return 0, false, err
	}
	sz, ok := size[typ]
	if !ok {
		return 0, false, fmt.Errorf("unknown GGUF value type %d", typ)
	}
	b, err := r.take(sz)
	if err != nil {
		return 0, false, err
	}
	switch typ {
	case ggufU8:
		return uint64(b[0]), true, nil
	case ggufI8:
		return nonNeg(int64(int8(b[0]))), true, nil
	case ggufU16:
		return uint64(binary.LittleEndian.Uint16(b)), true, nil
	case ggufI16:
		return nonNeg(int64(int16(binary.LittleEndian.Uint16(b)))), true, nil
	case ggufU32:
		return uint64(binary.LittleEndian.Uint32(b)), true, nil
	case ggufI32:
		return nonNeg(int64(int32(binary.LittleEndian.Uint32(b)))), true, nil
	case ggufU64:
		return binary.LittleEndian.Uint64(b), true, nil
	case ggufI64:
		return nonNeg(int64(binary.LittleEndian.Uint64(b))), true, nil
	}
	return 0, false, nil
}

func nonNeg(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// kvBytesPerToken estimates the memory the context cache needs for each token, at Ollama's default 16-bit cache. It returns 0 when the file didn't say enough.
func (h Header) kvBytesPerToken() uint64 {
	a := h.Arch + "."
	layers := h.Numbers[a+"block_count"]
	kvHeads := h.Numbers[a+"attention.head_count_kv"]
	if kvHeads == 0 {
		kvHeads = h.Numbers[a+"attention.head_count"]
	}
	keyLen, valLen := h.Numbers[a+"attention.key_length"], h.Numbers[a+"attention.value_length"]
	if keyLen == 0 {
		if heads := h.Numbers[a+"attention.head_count"]; heads > 0 {
			keyLen = h.Numbers[a+"embedding_length"] / heads
		}
	}
	if valLen == 0 {
		valLen = keyLen
	}
	if layers == 0 || kvHeads == 0 || keyLen == 0 {
		return 0
	}
	return layers * kvHeads * (keyLen + valLen) * 2
}

// tagRef is a parsed Ollama model name.
type tagRef struct {
	Namespace, Name, Tag string
}

func (t tagRef) String() string {
	if t.Namespace == "library" {
		return t.Name + ":" + t.Tag
	}
	return t.Namespace + "/" + t.Name + ":" + t.Tag
}

// Canonical adds ":latest" when a name has no tag, the way Ollama does.
func Canonical(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return ""
	}
	if !strings.Contains(tag[strings.LastIndex(tag, "/")+1:], ":") {
		tag += ":latest"
	}
	return tag
}

func parseTag(tag string) (tagRef, error) {
	c := Canonical(tag)
	if err := ValidTag(c); err != nil {
		return tagRef{}, err
	}
	path, t, _ := strings.Cut(c, ":")
	ns, name, ok := strings.Cut(path, "/")
	if !ok {
		ns, name = "library", path
	}
	return tagRef{Namespace: ns, Name: name, Tag: t}, nil
}

// ValidTag rejects things that aren't plain local Ollama model names: cloud models, Hugging Face imports, links, and commands.
func ValidTag(tag string) error {
	l := strings.ToLower(tag)
	switch {
	case l == "":
		return errors.New("no model name")
	case strings.Contains(l, "://") || strings.HasPrefix(l, "www."):
		return errors.New("that's a link, not a model name")
	case strings.HasPrefix(l, "hf.co/") || strings.HasPrefix(l, "huggingface.co/"):
		return errors.New("Hugging Face imports aren't supported; see the README's Advanced section for llama.cpp")
	case strings.Contains(l, "cloud"):
		return errors.New("cloud models send your code to a server, so they aren't allowed")
	case strings.ContainsAny(l, " \t;&|$`'\"<>()"):
		return errors.New("that looks like a command, not a model name")
	}
	for _, r := range l {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-:/", r)) {
			return fmt.Errorf("%q isn't a valid model name", tag)
		}
	}
	if strings.Count(l, "/") > 1 || strings.Count(l, ":") > 1 {
		return fmt.Errorf("%q isn't a valid model name", tag)
	}
	return nil
}
