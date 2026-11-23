package config

import (
	"context"
	"regexp"
	"testing"
)

// The Terraform read functions of every resource look the object up by the
// bare ID the API assigned on create, and upjet never calls the Terraform
// importers. Any prefix or composite form in the external name would make the
// resource unfindable after an import or a provider restart.
func TestExternalNameIsBareTerraformID(t *testing.T) {
	params := map[string]any{"cluster_id": "42"}
	for name, e := range ExternalNameConfigs {
		t.Run(name, func(t *testing.T) {
			id, err := e.GetIDFn(context.Background(), "object-id", params, nil)
			if err != nil {
				t.Fatalf("GetIDFn: %v", err)
			}
			if id != "object-id" {
				t.Errorf("GetIDFn: want %q, got %q", "object-id", id)
			}
			got, err := e.GetExternalNameFn(map[string]any{"id": "object-id"})
			if err != nil {
				t.Fatalf("GetExternalNameFn: %v", err)
			}
			if got != "object-id" {
				t.Errorf("GetExternalNameFn: want %q, got %q", "object-id", got)
			}
		})
	}
}

// The include lists are unanchored regular expressions, so every entry must
// match its own resource only (e.g. scylladbcloud_cluster must not also match
// scylladbcloud_cluster_connection).
func TestExternalNameConfiguredMatchesOnlyItsResource(t *testing.T) {
	for _, pattern := range ExternalNameConfigured() {
		re := regexp.MustCompile(pattern)
		matches := 0
		for name := range ExternalNameConfigs {
			if re.MatchString(name) {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("pattern %q matches %d configured resources, want 1", pattern, matches)
		}
	}
}
