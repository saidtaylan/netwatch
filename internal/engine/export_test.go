// export_test.go — re-exports unexported symbols so that tests/engine_test can
// call internal helpers directly (white-box via Go's export-for-test pattern).
// This file is ONLY compiled during `go test` runs; it does not affect the
// binary.
package engine

// Config-level helpers
var (
	ExportValidateConfig      = validateConfig
	ExportValidateApps        = validateApps
	ExportBuildAppTargetIndex = buildAppTargetIndex
)

// Notification helpers
var (
	ExportMergeNotifyChannels = mergeNotifyChannels
	ExportBuildAppContext     = buildAppContext
)

// Topology helpers
var ExportBuildDependencyGraph = buildDependencyGraph

// SLO helpers
var (
	ExportNewSLOManager = newSLOManager
	ExportParseWindow   = parseWindow
)

// Config push helpers
var (
	ExportMergeSharedMap   = mergeSharedMap
	ExportExtractSharedMap = extractSharedMap
)
