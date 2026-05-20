package engine

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// watchdogState is an internal sentinel used by runWatchdog.
const (
	wdStateUnknown  = 0 // no scrape received yet since start
	wdStateHealthy  = 1 // Prometheus is scraping on time
	wdStateIsolated = 2 // scrape gap exceeded threshold
)

// NotifyScrape records the current time as the last successful /metrics scrape.
// It must be called from the /metrics HTTP handler on every request.
func (e *Engine) NotifyScrape() {
	e.lastScrapeNano.Store(time.Now().UnixNano())
}

// runWatchdog periodically checks whether Prometheus has scraped recently.
// When the gap since the last scrape exceeds watchdog_threshold_sec the agent
// logs a [WATCHDOG] warning and sets the network_probe_prometheus_connected
// gauge to 0. Once scraping resumes it logs [PROMETHEUS] and sets the gauge
// back to 1.
//
// The goroutine exits immediately when watchdog_threshold_sec is 0 (disabled).
func (e *Engine) runWatchdog(ctx context.Context) {
	e.mu.RLock()
	thresholdSec := e.cfg.watchdogThresholdSec()
	e.mu.RUnlock()

	if thresholdSec <= 0 {
		return // watchdog disabled
	}

	threshold := time.Duration(thresholdSec) * time.Second
	// Check every 1/3 of the threshold so we catch the transition quickly.
	checkInterval := threshold / 3
	if checkInterval < 5*time.Second {
		checkInterval = 5 * time.Second
	}

	var state atomic.Int32 // wdState* constants
	state.Store(wdStateUnknown)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lastNano := e.lastScrapeNano.Load()
			if lastNano == 0 {
				// No scrape received at all yet — stay unknown, don't alarm.
				continue
			}

			age := time.Since(time.Unix(0, lastNano))
			cur := state.Load()

			if age > threshold {
				// Prometheus appears to have stopped scraping.
				if cur != wdStateIsolated {
					state.Store(wdStateIsolated)
					GaugePrometheusConnected.Set(0)
					slog.Warn("[WATCHDOG] Prometheus scrape not detected",
						"threshold_sec", thresholdSec,
						"last_scrape_ago", age.Round(time.Second),
					)
				}
			} else {
				// Scraping is healthy.
				if cur == wdStateIsolated {
					state.Store(wdStateHealthy)
					GaugePrometheusConnected.Set(1)
					slog.Info("[PROMETHEUS] Prometheus scraping resumed",
						"last_scrape_ago", age.Round(time.Second),
					)
				} else if cur == wdStateUnknown {
					state.Store(wdStateHealthy)
					GaugePrometheusConnected.Set(1)
				}
			}
		}
	}
}
