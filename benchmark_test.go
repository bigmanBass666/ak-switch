package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkProxyRequest benchmarks the proxy handler under normal conditions
// where the upstream responds successfully on every request.
func BenchmarkProxyRequest(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	alvus := setupAlvus(b, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := http.Get(alvus.URL + "/v1/models")
			if err != nil {
				b.Fatalf("request: %v", err)
			}
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
	})
}

// BenchmarkProxyAllKeysCooldown benchmarks when all keys are in cooldown.
// The upstream returns 429 for every request, forcing the proxy to exhaust all retries.
func BenchmarkProxyAllKeysCooldown(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	alvus := setupAlvus(b, upstream, []string{"key-a", "key-b"}, 3, 2)
	defer alvus.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(alvus.URL + "/v1/models")
		if err != nil {
			b.Fatalf("request: %v", err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkProxyFlakyUpstream benchmarks when the upstream is intermittent:
// requests alternate between success and failure.
func BenchmarkProxyFlakyUpstream(b *testing.B) {
	var callCount int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount%2 == 0 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer upstream.Close()

	alvus := setupAlvus(b, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 5, 2)
	defer alvus.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := http.Get(alvus.URL + "/v1/models")
			if err != nil {
				b.Fatalf("request: %v", err)
			}
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
	})
}
