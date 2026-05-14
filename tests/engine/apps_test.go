//go:build !windows

package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saidtaylan/netwatch/internal/engine"
)

// writeAppsConfig is a helper that writes YAML to a temp file and returns the path.
func writeAppsConfig(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writeAppsConfig: %v", err)
	}
	return p
}

// TestApps_DuplicateAppName verifies that duplicate app names produce a validation error.
func TestApps_DuplicateAppName(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19100"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "svc-a"
    name: "Service A"
    type: tcp
    target: "127.0.0.1:9900"

apps:
  - name: "my-app"
    uses: ["svc-a"]
  - name: "my-app"
    uses: ["svc-a"]
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for duplicate app name")
	}
}

// TestApps_UnknownTargetRef verifies that app.uses referencing nonexistent target → error.
func TestApps_UnknownTargetRef(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19101"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "svc-a"
    name: "Service A"
    type: tcp
    target: "127.0.0.1:9901"

apps:
  - name: "my-app"
    uses: ["nonexistent-target-xyz"]
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for app.uses referencing nonexistent target")
	}
}

// TestApps_ValidConfiguration verifies that a well-formed apps config passes validation.
func TestApps_ValidConfiguration(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19102"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "svc-db"
    name: "Database"
    type: tcp
    target: "127.0.0.1:9902"
  - id: "svc-api"
    name: "API"
    type: tcp
    target: "127.0.0.1:9903"

apps:
  - name: "payment-gateway"
    owner_team: "fintech-sre"
    uses: ["svc-db", "svc-api"]
    notifications: ["noop"]
  - name: "inventory-service"
    owner_team: "logistics-dev"
    uses: ["svc-db"]
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for valid apps config: %v", err)
	}
	if len(cfg.Apps) != 2 {
		t.Errorf("expected 2 apps, got %d", len(cfg.Apps))
	}
}

// TestApps_AppWithNoUses verifies that an app with empty uses list → error.
func TestApps_AppWithNoUses(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19103"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "svc-a"
    name: "Service A"
    type: tcp
    target: "127.0.0.1:9904"

apps:
  - name: "empty-app"
    uses: []
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for app with empty uses list")
	}
}

// TestApps_AppChannelUndefined verifies error when app.notifications references undefined channel.
func TestApps_AppChannelUndefined(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19104"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "svc-a"
    name: "Service A"
    type: tcp
    target: "127.0.0.1:9905"

apps:
  - name: "my-app"
    uses: ["svc-a"]
    notifications: ["undefined-channel-xyz"]
`)
	_, err := engine.ValidateConfigFile(p)
	if err == nil {
		t.Fatal("expected error for app referencing undefined notification channel")
	}
}

// TestApps_StructFields verifies that App struct fields are as expected.
func TestApps_StructFields(t *testing.T) {
	app := engine.App{
		Name:          "test-app",
		OwnerTeam:     "platform-eng",
		Uses:          []string{"target-1", "target-2"},
		Notifications: []string{"pagerduty", "slack"},
	}
	if app.Name != "test-app" {
		t.Errorf("Name: want test-app, got %q", app.Name)
	}
	if app.OwnerTeam != "platform-eng" {
		t.Errorf("OwnerTeam: want platform-eng, got %q", app.OwnerTeam)
	}
	if len(app.Uses) != 2 {
		t.Errorf("Uses: want 2 entries, got %d", len(app.Uses))
	}
	if len(app.Notifications) != 2 {
		t.Errorf("Notifications: want 2 entries, got %d", len(app.Notifications))
	}
}

// TestApps_NoAppsSection verifies that a config without apps section passes.
func TestApps_NoAppsSection(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19105"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "svc-a"
    name: "Service A"
    type: tcp
    target: "127.0.0.1:9906"
`)
	cfg, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("unexpected error for config without apps: %v", err)
	}
	if len(cfg.Apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(cfg.Apps))
	}
}

// TestApps_TargetIDRef verifies apps can reference targets by their ID field.
func TestApps_TargetIDRef(t *testing.T) {
	p := writeAppsConfig(t, `
port: "19106"
timeout: 5
max_retries: 1
retry_interval_sec: 5
probe_interval_sec: 30
ticker_interval_sec: 1
reload_interval_sec: 0

notifications:
  noop:
    type: script
    parameters:
      script: "/bin/true"

default_notify: ["noop"]

targets:
  - id: "db-primary"
    name: "DB Primary"
    type: tcp
    target: "127.0.0.1:9907"

apps:
  - name: "data-pipeline"
    uses: ["db-primary"]
`)
	_, err := engine.ValidateConfigFile(p)
	if err != nil {
		t.Fatalf("expected no error when app uses target by ID: %v", err)
	}
}
