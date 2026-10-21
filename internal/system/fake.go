package system

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// FakeRunner records commands instead of running them. It backs --dry-run and tests.
type FakeRunner struct {
	mu    sync.Mutex
	Calls []Cmd
	// Responses maps a command-line prefix to canned output. The longest matching prefix wins.
	Responses map[string]string
	// Failures maps a command-line prefix to an error message.
	Failures map[string]string
}

func (f *FakeRunner) Run(_ context.Context, c Cmd) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, c)
	line := c.String()
	if msg, ok := longestPrefix(f.Failures, line); ok {
		return "", errors.New(msg)
	}
	out, _ := longestPrefix(f.Responses, line)
	return out, nil
}

// Lines returns every recorded command as a string.
func (f *FakeRunner) Lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		out[i] = c.String()
	}
	return out
}

func longestPrefix(m map[string]string, line string) (string, bool) {
	best, val, found := -1, "", false
	for k, v := range m {
		if strings.HasPrefix(line, k) && len(k) > best {
			best, val, found = len(k), v, true
		}
	}
	return val, found
}
