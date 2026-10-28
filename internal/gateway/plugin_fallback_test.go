package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A plugin with another candidate after it (in a group or via fallback) is
// told so with X-Magpie-Fallback: 1, so plugins that can yield on long
// queues (like Qoder or CodeArts) fail over immediately instead of waiting.
// The last candidate is never told so, and the header never reaches the vendor.
func TestPluginFallbackHeader(t *testing.T) {
	var mu sync.Mutex
	var gotFallback []string

	pid := besideFake(t, "fakeco", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		gotFallback = append(gotFallback, r.Header.Get("X-Saw-Fallback"))
		mu.Unlock()
		if v := r.Header.Get(provider.FallbackHeader); v != "" {
			t.Errorf("vendor received %s: %q", provider.FallbackHeader, v)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","model":"fake-1","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))

	other := &keyed{}
	serveOn(t, "other", "ko", []string{"m"}, other)

	s := New()

	// 1. Group with plugin first, another candidate second: plugin should receive fallback=1
	if err := provider.SaveGroup(provider.Group{Name: "WithFallback", Members: []string{pid + "/fake-1", "other/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotFallback = nil
	mu.Unlock()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"group/withfallback","messages":[{"role":"user","content":"hi"}]}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("group/withfallback code %d: %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	if len(gotFallback) != 1 || gotFallback[0] != "1" {
		t.Errorf("with next candidate: got fallback %v, want [1]", gotFallback)
	}
	gotFallback = nil
	mu.Unlock()

	// 2. Solo plugin or last in group: plugin should receive no fallback header ("")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+pid+`/fake-1","messages":[{"role":"user","content":"hi"}]}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("solo plugin code %d: %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	if len(gotFallback) != 1 || gotFallback[0] != "" {
		t.Errorf("solo candidate: got fallback %v, want [\"\"]", gotFallback)
	}
	mu.Unlock()
}
