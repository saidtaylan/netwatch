package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saidtaylan/netwatch/internal/storage"
)

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// ── MigrateState ────────────────────────────────────────────────────────────

func TestMigrateState_NoFile(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	res, err := MigrateState(context.Background(), mem, dir, "node-1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.Skipped {
		t.Errorf("missing file should result in Skipped=true")
	}
	if res.Records != 0 {
		t.Errorf("no records expected")
	}
}

func TestMigrateState_V2(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	writeJSON(t, dir, "state.json", map[string]any{
		"version": 2,
		"targets": map[string]any{
			"db-primary": map[string]any{
				"state":      "hard_down",
				"seq":        7,
				"error_code": "connection refused",
			},
			"api-gateway": map[string]any{
				"state": "up",
				"seq":   3,
			},
		},
	})

	res, err := MigrateState(context.Background(), mem, dir, "node-1")
	if err != nil {
		t.Fatalf("MigrateState: %v", err)
	}
	if res.Records != 2 {
		t.Errorf("expected 2 records, got %d", res.Records)
	}
	if res.ArchivedAs == "" {
		t.Errorf("ArchivedAs should be set")
	}

	// Verify records in backend
	rec, err := mem.Get(context.Background(), storage.TableTargetStates, "db-primary")
	if err != nil {
		t.Fatalf("get db-primary: %v", err)
	}
	if rec.Version.Seq != 7 {
		t.Errorf("seq mismatch: %d", rec.Version.Seq)
	}
	if !strings.Contains(string(rec.Payload), "hard_down") {
		t.Errorf("payload missing state: %q", string(rec.Payload))
	}

	// Verify archive
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("state.json should be removed after archive")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json.migrated")); err != nil {
		t.Errorf("state.json.migrated should exist: %v", err)
	}
}

func TestMigrateState_V1Fallback(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	// v1 format: plain map[string]bool
	writeJSON(t, dir, "state.json", map[string]bool{
		"target-a": true,
		"target-b": false,
	})

	res, err := MigrateState(context.Background(), mem, dir, "node-1")
	if err != nil {
		t.Fatalf("MigrateState v1: %v", err)
	}
	if res.Records != 2 {
		t.Errorf("expected 2 records, got %d", res.Records)
	}

	// target-b should be hard_down (false)
	rec, _ := mem.Get(context.Background(), storage.TableTargetStates, "target-b")
	if !strings.Contains(string(rec.Payload), "hard_down") {
		t.Errorf("v1 false didn't map to hard_down: %q", string(rec.Payload))
	}
}

func TestMigrateState_Idempotent(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	writeJSON(t, dir, "state.json", map[string]any{
		"version": 2,
		"targets": map[string]any{
			"x": map[string]any{"state": "up", "seq": 1},
		},
	})

	// First run — migrates
	if _, err := MigrateState(context.Background(), mem, dir, "n"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Second run — file is gone, should skip cleanly
	res, err := MigrateState(context.Background(), mem, dir, "n")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !res.Skipped {
		t.Errorf("second run should be Skipped")
	}
}

func TestMigrateState_BadJSON(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := MigrateState(context.Background(), mem, dir, "n")
	if err == nil {
		t.Error("expected parse error")
	}
	if res.ParseError == nil {
		t.Error("ParseError should be set")
	}
	// File must NOT be archived on parse failure
	if _, err := os.Stat(filepath.Join(dir, "state.json")); os.IsNotExist(err) {
		t.Errorf("state.json should be preserved on parse error")
	}
}

// ── MigrateIncidents ────────────────────────────────────────────────────────

func TestMigrateIncidents_Envelope(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	started := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	ended := started.Add(15 * time.Minute)

	writeJSON(t, dir, "incidents.json", incidentFileV1{
		Version: 1,
		Incidents: []incidentRecord{
			{TargetID: "db-primary", StartedAt: started, EndedAt: &ended, DurationSec: 900, Scope: "GLOBAL"},
			{TargetID: "api-gateway", StartedAt: started.Add(time.Hour)},
		},
	})

	res, err := MigrateIncidents(context.Background(), mem, dir, "node-1")
	if err != nil {
		t.Fatalf("MigrateIncidents: %v", err)
	}
	if res.Records != 2 {
		t.Errorf("expected 2 records, got %d", res.Records)
	}

	// Record IDs follow <target_id>-<unix_started_at>
	expectedID := "db-primary-" + intToStr(int(started.Unix()))
	rec, err := mem.Get(context.Background(), storage.TableSLOIncidents, expectedID)
	if err != nil {
		t.Fatalf("incident not in storage: %v", err)
	}
	if !strings.Contains(string(rec.Payload), "GLOBAL") {
		t.Errorf("payload missing fields: %q", string(rec.Payload))
	}
}

func TestMigrateIncidents_PlainArray(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	// Some older formats wrote a plain array, no envelope
	now := time.Now().UTC()
	writeJSON(t, dir, "incidents.json", []incidentRecord{
		{TargetID: "x", StartedAt: now},
	})

	res, err := MigrateIncidents(context.Background(), mem, dir, "n")
	if err != nil {
		t.Fatalf("plain array: %v", err)
	}
	if res.Records != 1 {
		t.Errorf("plain array should yield 1 record")
	}
}

func TestMigrateIncidents_NoFile(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	res, err := MigrateIncidents(context.Background(), mem, t.TempDir(), "n")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Error("should be Skipped")
	}
}

// ── MigrateMaintenance ──────────────────────────────────────────────────────

func TestMigrateMaintenance(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	now := time.Now().UTC()
	writeJSON(t, dir, "maintenance.json", maintenanceFile{
		Version: 1,
		Windows: []maintenanceWindow{
			{
				ID:        "mw-1",
				TargetIDs: []string{"db-primary"},
				StartedAt: now,
				ExpiresAt: now.Add(time.Hour),
				Reason:    "DB upgrade",
			},
			{
				ID:        "mw-2",
				TargetIDs: []string{"api-gateway"},
				StartedAt: now.Add(-time.Hour),
				ExpiresAt: now,
			},
		},
	})

	res, err := MigrateMaintenance(context.Background(), mem, dir, "node-1")
	if err != nil {
		t.Fatalf("MigrateMaintenance: %v", err)
	}
	if res.Records != 2 {
		t.Errorf("expected 2 records, got %d", res.Records)
	}

	rec, err := mem.Get(context.Background(), storage.TableMaintenance, "mw-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec.Payload), "DB upgrade") {
		t.Errorf("payload missing fields")
	}
}

func TestMigrateMaintenance_SkipsEmptyID(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	now := time.Now().UTC()
	writeJSON(t, dir, "maintenance.json", maintenanceFile{
		Version: 1,
		Windows: []maintenanceWindow{
			{ID: "", StartedAt: now, ExpiresAt: now.Add(time.Hour)}, // malformed
			{ID: "valid", StartedAt: now, ExpiresAt: now.Add(time.Hour)},
		},
	})

	res, _ := MigrateMaintenance(context.Background(), mem, dir, "n")
	if res.Records != 1 {
		t.Errorf("expected 1 valid record, got %d (malformed should be skipped)", res.Records)
	}
}

// ── RunAll ──────────────────────────────────────────────────────────────────

func TestRunAll_NoFiles(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	results, err := RunAll(context.Background(), mem, t.TempDir(), "node-1")
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results (one per file), got %d", len(results))
	}
	for _, r := range results {
		if !r.Skipped {
			t.Errorf("all should skip when no files present: %+v", r)
		}
	}
}

func TestRunAll_MixedFiles(t *testing.T) {
	dir := t.TempDir()
	mem := storage.NewMemoryStorage()
	defer mem.Close()

	// Only state.json exists
	writeJSON(t, dir, "state.json", map[string]any{
		"version": 2,
		"targets": map[string]any{
			"a": map[string]any{"state": "up", "seq": 1},
		},
	})

	results, err := RunAll(context.Background(), mem, dir, "node-1")
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	gotMigrated := 0
	gotSkipped := 0
	for _, r := range results {
		if r.Skipped {
			gotSkipped++
		} else {
			gotMigrated++
		}
	}
	if gotMigrated != 1 || gotSkipped != 2 {
		t.Errorf("expected 1 migrated + 2 skipped, got %d + %d", gotMigrated, gotSkipped)
	}
}

func TestRunAll_EmptyDataDir(t *testing.T) {
	mem := storage.NewMemoryStorage()
	defer mem.Close()
	_, err := RunAll(context.Background(), mem, "", "n")
	if err == nil {
		t.Error("expected error for empty data_dir")
	}
}

// helper — convert int to string without strconv import to keep this file lean
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
