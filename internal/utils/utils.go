package utils

import (
	"net/http"
	"strings"
)

type LogEntry struct {
	Timestamp       string `json:"timestamp"`
	Key             string `json:"key"`
	KeyIndex        int    `json:"key_index"`
	KeyName         string `json:"key_name"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	Status          int    `json:"status"`
	RequestBodySize int    `json:"request_body_size"`
	DurationMs      int64  `json:"duration_ms,omitempty"`
	Attempt         int    `json:"attempt,omitempty"`
	Provider        string `json:"provider,omitempty"`
}

func MaskKey(key string) string {
	if len(key) <= 12 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func CopyHeaders(dst, src http.Header) {
	for k, vals := range src {
		lower := strings.ToLower(k)
		if lower == "x-admin-token" || lower == "cookie" || lower == "proxy-authorization" {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}
