package engine

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"runtime"
	"strings"
)

// mailAlerter sends email notifications via SMTP.
//
// Required params: smtp_host, from, to
// Optional params: smtp_port, smtp_user, smtp_pass, subject_prefix,
//
//	tls_mode (starttls|tls|none), tls_insecure, ca_cert
type mailAlerter struct {
	host          string
	port          string
	user          string
	pass          string
	from          string
	to            []string
	subjectPrefix string
	tlsMode       string
	tlsInsecure   bool
	caCert        string
	// lastEnv is set by Send() so buildMessage can access the full env map
	// when generating the HTML body. Not safe for concurrent use, but Send()
	// is always called from a fresh goroutine per alert.
	lastEnv map[string]string
}

func newMailAlerter(params map[string]string) (*mailAlerter, error) {
	host := params["smtp_host"]
	if host == "" {
		return nil, fmt.Errorf("mail: smtp_host is required")
	}
	from := params["from"]
	if from == "" {
		return nil, fmt.Errorf("mail: from is required")
	}
	toStr := params["to"]
	if toStr == "" {
		return nil, fmt.Errorf("mail: to is required")
	}
	to := parseAddresses(toStr)
	if len(to) == 0 {
		return nil, fmt.Errorf("mail: to contains no valid addresses")
	}
	mode := params["tls_mode"]
	if mode == "" {
		mode = "starttls"
	}
	switch mode {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("mail: tls_mode must be starttls, tls, or none; got %q", mode)
	}
	port := params["smtp_port"]
	if port == "" {
		switch mode {
		case "tls":
			port = "465"
		case "none":
			port = "25"
		default:
			port = "587"
		}
	}
	return &mailAlerter{
		host:          host,
		port:          port,
		user:          params["smtp_user"],
		pass:          params["smtp_pass"],
		from:          from,
		to:            to,
		subjectPrefix: params["subject_prefix"],
		tlsMode:       mode,
		tlsInsecure:   params["tls_insecure"] == "true",
		caCert:        params["ca_cert"],
	}, nil
}

func (m *mailAlerter) Send(env map[string]string) error {
	m.lastEnv = env
	subject := m.buildSubject(env)
	body := m.buildBody(env)
	msg := m.buildMessage(subject, body)
	addr := net.JoinHostPort(m.host, m.port)
	switch m.tlsMode {
	case "tls":
		return m.sendImplicitTLS(addr, msg)
	case "none":
		return m.sendPlain(addr, msg)
	default:
		return m.sendStartTLS(addr, msg)
	}
}

func (m *mailAlerter) buildSubject(env map[string]string) string {
	prefix := m.subjectPrefix
	if prefix == "" {
		prefix = "[ALERT]"
	}
	return fmt.Sprintf("%s %s — %s", prefix, env["NAME"], strings.ToUpper(env["STATUS"]))
}

func (m *mailAlerter) buildBody(env map[string]string) string {
	plain := fmt.Sprintf(
		"Name:       %s\nTarget:     %s\nType:       %s\nStatus:     %s\nHost:       %s\nPort:       %s\nAgent:      %s\nNode:       %s\nSeq:        %s\n",
		env["NAME"], env["TARGET"], env["TYPE"],
		strings.ToUpper(env["STATUS"]),
		env["HOST"], env["PORT"], env["APP_NAME"],
		env["NODE_NAME"], env["SEQ"],
	)
	if v := env["ERROR_CODE"]; v != "" {
		plain += "Error:      " + v + "\n"
	}
	if v := env["AFFECTED_APPS"]; v != "" {
		plain += "Apps:       " + v + "\n"
		plain += "Teams:      " + env["OWNER_TEAMS"] + "\n"
	}
	return plain
}

// buildHTMLBody renders an HTML alert email. When AFFECTED_APPS is present a
// table of impacted applications is included so on-call engineers can triage
// without opening a separate dashboard.
func (m *mailAlerter) buildHTMLBody(env map[string]string) string {
	status := strings.ToUpper(env["STATUS"])
	color := "#c0392b" // red for down
	if env["STATUS"] == "reachable" {
		color = "#27ae60" // green for up
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><body style="font-family:Arial,sans-serif;font-size:14px">`)
	sb.WriteString(fmt.Sprintf(`<h2 style="color:%s">%s — %s</h2>`, color, env["NAME"], status))
	sb.WriteString(`<table style="border-collapse:collapse">`)

	rows := [][2]string{
		{"Target", env["TARGET"]},
		{"Type", env["TYPE"]},
		{"Host", env["HOST"]},
		{"Port", env["PORT"]},
		{"Agent", env["APP_NAME"]},
		{"Node", env["NODE_NAME"]},
		{"Seq", env["SEQ"]},
	}
	if v := env["ERROR_CODE"]; v != "" {
		rows = append(rows, [2]string{"Error", v})
	}
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf(
			`<tr><td style="padding:4px 12px 4px 0;font-weight:bold;color:#555">%s</td>`+
				`<td style="padding:4px 0">%s</td></tr>`,
			r[0], r[1],
		))
	}
	sb.WriteString(`</table>`)

	if apps := env["AFFECTED_APPS"]; apps != "" {
		sb.WriteString(`<h3 style="margin-top:16px">Affected Applications</h3>`)
		sb.WriteString(`<table style="border-collapse:collapse;width:100%">`)
		sb.WriteString(`<tr style="background:#f2f2f2">` +
			`<th style="padding:6px 12px;text-align:left;border:1px solid #ddd">Application</th>` +
			`<th style="padding:6px 12px;text-align:left;border:1px solid #ddd">Owner Team</th></tr>`)
		appList := strings.Split(apps, ",")
		teamList := strings.Split(env["OWNER_TEAMS"], ",")
		for i, app := range appList {
			team := ""
			if i < len(teamList) {
				team = strings.TrimSpace(teamList[i])
			}
			sb.WriteString(fmt.Sprintf(
				`<tr><td style="padding:6px 12px;border:1px solid #ddd">%s</td>`+
					`<td style="padding:6px 12px;border:1px solid #ddd">%s</td></tr>`,
				strings.TrimSpace(app), team,
			))
		}
		sb.WriteString(`</table>`)
	}

	sb.WriteString(`</body></html>`)
	return sb.String()
}

// mailBoundary uses BinaryName so rebranded builds produce consistent MIME output.
var mailBoundary = "==" + BinaryName + "_mime_boundary=="

func (m *mailAlerter) buildMessage(subject, plainBody string) []byte {
	htmlBody := m.buildHTMLBody(m.lastEnv)

	var sb strings.Builder
	sb.WriteString("From: " + m.from + "\r\n")
	sb.WriteString("To: " + strings.Join(m.to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: multipart/alternative; boundary=\"" + mailBoundary + "\"\r\n")
	sb.WriteString("\r\n")

	// Plain text part.
	sb.WriteString("--" + mailBoundary + "\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	sb.WriteString(strings.ReplaceAll(plainBody, "\n", "\r\n"))
	sb.WriteString("\r\n")

	// HTML part (preferred; MUAs pick the last matching part).
	sb.WriteString("--" + mailBoundary + "\r\n")
	sb.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	sb.WriteString(htmlBody)
	sb.WriteString("\r\n")

	sb.WriteString("--" + mailBoundary + "--\r\n")
	return []byte(sb.String())
}

func (m *mailAlerter) auth() smtp.Auth {
	if m.user == "" {
		return nil
	}
	return smtp.PlainAuth("", m.user, m.pass, m.host)
}

func (m *mailAlerter) tlsCfg() *tls.Config {
	if m.tlsInsecure {
		return &tls.Config{ServerName: m.host, InsecureSkipVerify: true} //nolint:gosec
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return &tls.Config{ServerName: m.host}
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if m.caCert != "" {
		if pem, err := os.ReadFile(m.caCert); err == nil {
			pool.AppendCertsFromPEM(pem)
		}
	}
	return &tls.Config{ServerName: m.host, RootCAs: pool}
}

func (m *mailAlerter) sendStartTLS(addr string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("mail STARTTLS dial: %w", err)
	}
	defer c.Quit() //nolint:errcheck
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(m.tlsCfg()); err != nil {
			return fmt.Errorf("mail STARTTLS upgrade: %w", err)
		}
	}
	if a := m.auth(); a != nil {
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("mail SMTP auth: %w", err)
		}
	}
	return deliverMail(c, m.from, m.to, msg)
}

func (m *mailAlerter) sendImplicitTLS(addr string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, m.tlsCfg())
	if err != nil {
		return fmt.Errorf("mail TLS dial: %w", err)
	}
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("mail SMTP client: %w", err)
	}
	defer c.Quit() //nolint:errcheck
	if a := m.auth(); a != nil {
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("mail SMTP auth: %w", err)
		}
	}
	return deliverMail(c, m.from, m.to, msg)
}

func (m *mailAlerter) sendPlain(addr string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("mail plain dial: %w", err)
	}
	defer c.Quit() //nolint:errcheck
	if a := m.auth(); a != nil {
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("mail SMTP auth: %w", err)
		}
	}
	return deliverMail(c, m.from, m.to, msg)
}

// deliverMail issues MAIL FROM / RCPT TO / DATA on an open SMTP client.
func deliverMail(c *smtp.Client, from string, to []string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", addr, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return w.Close()
}

// parseAddresses splits a comma-separated address list and strips whitespace.
func parseAddresses(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if a := strings.TrimSpace(p); a != "" {
			out = append(out, a)
		}
	}
	return out
}

