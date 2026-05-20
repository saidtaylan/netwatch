package engine

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webhookAlerter sends an HTTP POST with a JSON body when an alert fires.
//
// Supported parameters (all in the notifications.<name>.parameters map):
//
//	url            (required) full HTTP/HTTPS endpoint to POST to
//	format         "generic" (default) | "alertmanager"
//	username       HTTP Basic Auth username (optional)
//	password       HTTP Basic Auth password (optional)
//	timeout_sec    request timeout in seconds (default 10)
//	tls_insecure   "true" to skip TLS verification (dev/test only)
//	header_<Name>  arbitrary request headers, e.g. header_Authorization: "Bearer tok"
//
// Generic format sends a single JSON object.
// Alertmanager format sends a JSON array compatible with the Prometheus
// Alertmanager v2 /api/v2/alerts endpoint.
type webhookAlerter struct {
	url         string
	format      string // "generic" | "alertmanager"
	headers     map[string]string
	username    string
	password    string
	client      *http.Client
}

// genericPayload is the "generic" webhook body.
type genericPayload struct {
	Name         string    `json:"name"`
	Target       string    `json:"target"`
	Host         string    `json:"host"`
	Port         string    `json:"port"`
	AppName      string    `json:"app_name"`
	NodeName     string    `json:"node_name,omitempty"`
	Status       string    `json:"status"`
	Type         string    `json:"type"`
	Seq          uint64    `json:"seq"`
	ErrorCode    string    `json:"error_code,omitempty"`
	AffectedApps string    `json:"affected_apps,omitempty"`
	OwnerTeams   string    `json:"owner_teams,omitempty"`
	FiredAt      time.Time `json:"fired_at"`
}

// amAlert is one entry in the Alertmanager v2 wire format.
type amAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      *time.Time        `json:"endsAt,omitempty"`
}

func newWebhookAlerter(params map[string]string) (*webhookAlerter, error) {
	url := params["url"]
	if url == "" {
		return nil, fmt.Errorf("webhook: url is required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("webhook: url must start with http:// or https://")
	}

	format := params["format"]
	if format == "" {
		format = "generic"
	}
	if format != "generic" && format != "alertmanager" {
		return nil, fmt.Errorf("webhook: unknown format %q (valid: generic, alertmanager)", format)
	}

	timeoutSec := 10
	if v := params["timeout_sec"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("webhook: invalid timeout_sec %q", v)
		}
		timeoutSec = n
	}

	tlsInsecure := params["tls_insecure"] == "true"
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: tlsInsecure}, //nolint:gosec
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   time.Duration(timeoutSec) * time.Second,
	}

	// Collect header_<Name> parameters as custom headers.
	headers := make(map[string]string)
	for k, v := range params {
		if strings.HasPrefix(k, "header_") {
			headers[strings.TrimPrefix(k, "header_")] = v
		}
	}

	return &webhookAlerter{
		url:      url,
		format:   format,
		headers:  headers,
		username: params["username"],
		password: params["password"],
		client:   client,
	}, nil
}

func (w *webhookAlerter) Send(env map[string]string) error {
	body, err := w.buildPayload(env)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}
	if w.username != "" {
		req.SetBasicAuth(w.username, w.password)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook POST %s: %w", w.url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook POST %s: unexpected status %d", w.url, resp.StatusCode)
	}
	return nil
}

func (w *webhookAlerter) buildPayload(env map[string]string) ([]byte, error) {
	now := time.Now().UTC()

	switch w.format {
	case "alertmanager":
		return w.buildAlertmanagerPayload(env, now)
	default:
		return w.buildGenericPayload(env, now)
	}
}

func (w *webhookAlerter) buildGenericPayload(env map[string]string, now time.Time) ([]byte, error) {
	seq, _ := strconv.ParseUint(env["SEQ"], 10, 64)
	p := genericPayload{
		Name:         env["NAME"],
		Target:       env["TARGET"],
		Host:         env["HOST"],
		Port:         env["PORT"],
		AppName:      env["APP_NAME"],
		NodeName:     env["NODE_NAME"],
		Status:       env["STATUS"],
		Type:         env["TYPE"],
		Seq:          seq,
		ErrorCode:    env["ERROR_CODE"],
		AffectedApps: env["AFFECTED_APPS"],
		OwnerTeams:   env["OWNER_TEAMS"],
		FiredAt:      now,
	}
	return json.Marshal(p)
}

func (w *webhookAlerter) buildAlertmanagerPayload(env map[string]string, now time.Time) ([]byte, error) {
	// Alertmanager uses "resolved" for recovery; active alerts have no endsAt.
	status := env["STATUS"]
	alertName := "ProbeDown"
	if status == "reachable" {
		alertName = "ProbeUp"
	}

	alert := amAlert{
		Labels: map[string]string{
			"alertname":   alertName,
			"name":        env["NAME"],
			"target":      env["TARGET"],
			"type":        env["TYPE"],
			"source_host": env["NODE_NAME"],
			"app_name":    env["APP_NAME"],
		},
		Annotations: map[string]string{
			"summary": fmt.Sprintf("Target %s is %s", env["NAME"], status),
		},
		StartsAt: now,
	}

	// Optional annotations — only set when non-empty so the AM UI stays clean.
	if v := env["ERROR_CODE"]; v != "" {
		alert.Annotations["error_code"] = v
	}
	if v := env["AFFECTED_APPS"]; v != "" {
		alert.Annotations["affected_apps"] = v
	}
	if v := env["OWNER_TEAMS"]; v != "" {
		alert.Annotations["owner_teams"] = v
	}
	if v := env["SEQ"]; v != "" {
		alert.Annotations["seq"] = v
	}

	// endsAt = now signals "resolved" to Alertmanager.
	if status == "reachable" {
		alert.EndsAt = &now
	}

	return json.Marshal([]amAlert{alert})
}
