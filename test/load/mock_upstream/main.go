package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
)

func main() {
	port := os.Getenv("MOCK_PORT")
	if port == "" {
		port = "19999"
	}

	flaky := os.Getenv("MOCK_FLAKY") == "1"
	var reqCount atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		count := reqCount.Add(1)

		if flaky && count%3 == 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintln(w, `{"error":"rate limit"}`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"id":"chatcmpl-mock","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Mock response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
	})

	addr := ":" + port
	log.Printf("Mock upstream listening on %s (flaky=%v)", addr, flaky)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("mock server: %v", err)
	}
}
