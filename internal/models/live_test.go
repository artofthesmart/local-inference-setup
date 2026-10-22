package models

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLiveShortlist checks the real shortlist against Ollama's registry. It only runs when COPILOT_LOCAL_LIVE=1, so normal test runs stay offline.
func TestLiveShortlist(t *testing.T) {
	if os.Getenv("COPILOT_LOCAL_LIVE") != "1" {
		t.Skip("set COPILOT_LOCAL_LIVE=1 to check the real registry")
	}
	list, _, err := LoadShortlist("")
	if err != nil {
		t.Fatal(err)
	}
	r := Registry{BaseURL: DefaultRegistry, HTTP: &http.Client{Timeout: 60 * time.Second}}
	got := r.CheckShortlist(t.Context(), list, 65536)
	for _, s := range got.Skipped {
		t.Errorf("skipped: %s", s)
	}
	for _, m := range got.OK {
		t.Logf("%-18s %-12s size %5.1f GiB  ctx %7d  kv/token %6d  need@64k %5.1f GiB", m.Tag, m.Arch, float64(m.Size)/gib, m.Context, m.KVPerToken, float64(m.MemoryNeed(65536))/gib)
	}
}
