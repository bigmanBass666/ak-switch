package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// expectedPanels lists the expected panel titles in the pre-built Grafana dashboard.
var expectedPanels = []string{
	"Request Rate",
	"Key Pool State",
	"Upstream Circuit Breaker",
	"Health Check Success Rate",
	"Request Latency (p50 / p95 / p99)",
	"Request Rate by Key Index",
	"Upstream Errors by Type",
	"Health Check Latency",
	"Request Status Distribution",
}

// TestGrafanaDashboardJSON_IsValid validates the pre-built Grafana dashboard JSON
// is valid and contains all expected panels with correct metadata.
func TestGrafanaDashboardJSON_IsValid(t *testing.T) {
	data, err := os.ReadFile("grafana/provisioning/dashboards/alvus-overview.json")
	if err != nil {
		t.Fatalf("failed to read dashboard JSON: %v", err)
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Required top-level fields
	cases := []struct {
		field    string
		expected string
	}{
		{"title", "Alvus Overview"},
		{"uid", "alvus-overview"},
	}
	for _, c := range cases {
		if v, ok := doc[c.field].(string); !ok || v != c.expected {
			t.Errorf("expected %s=%q, got %v", c.field, c.expected, doc[c.field])
		}
	}

	// Panel count and order
	panels, ok := doc["panels"].([]interface{})
	if !ok {
		t.Fatal("panels field is not an array")
	}
	if len(panels) != len(expectedPanels) {
		t.Fatalf("expected %d panels, got %d", len(expectedPanels), len(panels))
	}
	for i, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			t.Errorf("panel %d is not an object", i)
			continue
		}
		title, _ := panel["title"].(string)
		if title != expectedPanels[i] {
			t.Errorf("panel %d: expected title %q, got %q", i, expectedPanels[i], title)
		}
	}
}

// TestGrafanaDashboard_PanelsHaveTargets verifies every panel has at least one
// PromQL target expression and a non-empty expr field.
func TestGrafanaDashboard_PanelsHaveTargets(t *testing.T) {
	data, err := os.ReadFile("grafana/provisioning/dashboards/alvus-overview.json")
	if err != nil {
		t.Fatalf("failed to read dashboard JSON: %v", err)
	}

	var doc struct {
		Panels []struct {
			Title   string `json:"title"`
			Type    string `json:"type"`
			Targets []struct {
				Expr        string `json:"expr"`
				LegendFmt   string `json:"legendFormat"`
				RefID       string `json:"refId"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	for _, panel := range doc.Panels {
		if len(panel.Targets) == 0 {
			t.Errorf("panel %q (%s) has no targets", panel.Title, panel.Type)
			continue
		}
		for _, target := range panel.Targets {
			if strings.TrimSpace(target.Expr) == "" {
				t.Errorf("panel %q has target with empty expr", panel.Title)
			}
			if target.RefID == "" {
				t.Errorf("panel %q has target without refId", panel.Title)
			}
		}
	}
}

// TestPrometheusConfig_Exists validates the Prometheus config file exists
// and contains the expected scrape target for alvus.
func TestPrometheusConfig_Exists(t *testing.T) {
	data, err := os.ReadFile("prometheus/prometheus.yml")
	if err != nil {
		t.Fatalf("prometheus.yml not found: %v", err)
	}

	content := string(data)

	// Check for expected content patterns
	checks := []struct {
		pattern string
		desc    string
	}{
		{"alvus:3000", "scrape target alvus:3000"},
		{"/metrics", "metrics path /metrics"},
		{"scrape_interval: 15s", "scrape interval"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.pattern) {
			t.Errorf("prometheus.yml missing %s", c.desc)
		}
	}
}

// TestGrafanaProvisioning_ConfigsExist validates the Grafana provisioning
// configuration files exist and have expected content.
func TestGrafanaProvisioning_ConfigsExist(t *testing.T) {
	// Datasource config
	dsData, err := os.ReadFile("grafana/provisioning/datasources/prometheus.yml")
	if err != nil {
		t.Fatalf("datasource config not found: %v", err)
	}
	dsContent := string(dsData)
	dsChecks := []struct {
		pattern string
		desc    string
	}{
		{"Prometheus", "datasource name"},
		{"prometheus", "datasource type"},
		{"http://prometheus:9090", "prometheus URL"},
		{"isDefault: true", "default datasource"},
	}
	for _, c := range dsChecks {
		if !strings.Contains(dsContent, c.pattern) {
			t.Errorf("datasource config missing %s", c.desc)
		}
	}

	// Dashboard provisioning config
	dpData, err := os.ReadFile("grafana/provisioning/dashboards/dashboard.yml")
	if err != nil {
		t.Fatalf("dashboard provisioning config not found: %v", err)
	}
	dpContent := string(dpData)
	dpChecks := []struct {
		pattern string
		desc    string
	}{
		{"Alvus", "provider name"},
		{"file", "provisioning type"},
		{"/etc/grafana/provisioning/dashboards", "dashboard path"},
	}
	for _, c := range dpChecks {
		if !strings.Contains(dpContent, c.pattern) {
			t.Errorf("dashboard provisioning config missing %s", c.desc)
		}
	}
}

// TestDockerCompose_MinimalServices validates docker-compose.yml contains
// the expected service definitions.
func TestDockerCompose_MinimalServices(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("docker-compose.yml not found: %v", err)
	}

	content := string(data)

	// Check for service names
	services := []struct {
		name    string
		pattern string
		desc    string
	}{
		{"alvus", "alvus:", "alvus service"},
		{"prometheus", "prometheus:", "prometheus service"},
		{"grafana", "grafana:", "grafana service"},
	}
	for _, s := range services {
		if !strings.Contains(content, s.pattern) {
			t.Errorf("docker-compose.yml missing %s", s.desc)
		}
	}

	// Check for volumes
	volumePatterns := []struct {
		pattern string
		desc    string
	}{
		{"alvus-data", "alvus data volume"},
		{"prometheus-data", "prometheus data volume"},
		{"grafana-data", "grafana data volume"},
	}
	for _, v := range volumePatterns {
		if !strings.Contains(content, v.pattern) {
			t.Errorf("docker-compose.yml missing %s", v.desc)
		}
	}
}
