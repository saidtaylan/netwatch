package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"

	mysqldrv "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
)

// sql.go — the SQL Checker. A target is "up" when a connection to the database
// succeeds and an optional liveness query runs without error. Implements the
// Checker interface; the target is "host:port" and the connection details
// (driver, credentials, database/service, TLS) come from the JSON options. The
// DSN is built per driver by makeDSN — operators never write a raw DSN.

var supportedDrivers = map[string]bool{
	"mysql": true, "postgres": true, "mssql": true, "oracle": true,
}

var pgSSLModes = map[string]bool{
	"disable": true, "require": true, "verify-ca": true, "verify-full": true,
}

// sqlOptions holds options for sql-type targets.
type sqlOptions struct {
	Driver      string `json:"driver"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Database    string `json:"database,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Query       string `json:"query,omitempty"`
	SSLMode     string `json:"ssl_mode,omitempty"`
	TLSInsecure bool   `json:"tls_insecure,omitempty"`
}

// sqlChecker implements Checker for sql-type targets.
// The target field must be "host:port".
type sqlChecker struct{}

// Run opens a database connection to addr ("host:port") using a per-driver DSN
// built from the JSON options, then verifies liveness: it runs opts.Query (if
// set) and accepts any result including no rows, otherwise it pings the
// connection. Returns (true, nil) on success or (false, err) on a DSN, open,
// query or ping failure. The pool is capped at a single short-lived connection.
func (c *sqlChecker) Run(ctx context.Context, addr string, raw json.RawMessage) (bool, error) {
	var opts sqlOptions
	if err := json.Unmarshal(raw, &opts); err != nil {
		return false, fmt.Errorf("sql options: %w", err)
	}
	dsn, driver, err := makeDSN(addr, &opts)
	if err != nil {
		return false, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return false, fmt.Errorf("sql open: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	if opts.Query != "" {
		row := db.QueryRowContext(ctx, opts.Query)
		var v interface{}
		if err := row.Scan(&v); err != nil && err != sql.ErrNoRows {
			return false, fmt.Errorf("sql query: %w", err)
		}
	} else {
		if err := db.PingContext(ctx); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ValidateOptions checks the sql options at config-load time. SQL options are
// mandatory (unlike other types), so empty/null is rejected; it then rejects
// unknown fields and delegates the per-driver rules to checkSQLOptions.
func (c *sqlChecker) ValidateOptions(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("sql options: driver, username and password are required")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var opts sqlOptions
	if err := dec.Decode(&opts); err != nil {
		return fmt.Errorf("invalid sql options: %w", err)
	}
	return checkSQLOptions(opts)
}

// ParseAddr splits the "host:port" target into host and port for the alert env.
func (c *sqlChecker) ParseAddr(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("sql: target must be host:port, got %q: %w", addr, err)
	}
	return host, port, nil
}

// checkSQLOptions applies the per-driver validation rules: a supported driver
// and username are always required; Oracle needs a database (SID) or
// service_name and rejects TLS options; Postgres/MySQL/MSSQL need a database and
// reject Oracle's service_name; and the TLS knobs (ssl_mode vs tls_insecure) are
// constrained to what each driver actually supports. Returns the first violation.
func checkSQLOptions(o sqlOptions) error {
	if o.Driver == "" {
		return fmt.Errorf("sql: driver is required (mysql, postgres, mssql, oracle)")
	}
	if !supportedDrivers[o.Driver] {
		return fmt.Errorf("sql: unsupported driver %q", o.Driver)
	}
	if o.Username == "" {
		return fmt.Errorf("sql: username is required")
	}
	switch o.Driver {
	case "oracle":
		if o.Database == "" && o.ServiceName == "" {
			return fmt.Errorf("sql (oracle): database (SID) or service_name is required")
		}
		if o.SSLMode != "" {
			return fmt.Errorf("sql (oracle): ssl_mode is not supported")
		}
		if o.TLSInsecure {
			return fmt.Errorf("sql (oracle): tls_insecure is not supported")
		}
	case "postgres":
		if o.Database == "" {
			return fmt.Errorf("sql (postgres): database is required")
		}
		if o.ServiceName != "" {
			return fmt.Errorf("sql (postgres): service_name is oracle-specific")
		}
		if o.TLSInsecure && o.SSLMode != "" {
			return fmt.Errorf("sql (postgres): cannot set both tls_insecure and ssl_mode")
		}
		if o.SSLMode != "" && !pgSSLModes[o.SSLMode] {
			return fmt.Errorf("sql (postgres): ssl_mode %q invalid; use: disable, require, verify-ca, verify-full", o.SSLMode)
		}
	case "mysql":
		if o.Database == "" {
			return fmt.Errorf("sql (mysql): database is required")
		}
		if o.ServiceName != "" {
			return fmt.Errorf("sql (mysql): service_name is oracle-specific")
		}
		if o.SSLMode != "" {
			return fmt.Errorf("sql (mysql): ssl_mode is not valid; use tls_insecure: true instead")
		}
	case "mssql":
		if o.Database == "" {
			return fmt.Errorf("sql (mssql): database is required")
		}
		if o.ServiceName != "" {
			return fmt.Errorf("sql (mssql): service_name is oracle-specific")
		}
		if o.SSLMode != "" {
			return fmt.Errorf("sql (mssql): ssl_mode is not valid; use tls_insecure: true instead")
		}
	}
	return nil
}

// makeDSN builds the driver-specific connection string (and the database/sql
// driver name) from the "host:port" target and the options. It encodes
// credentials, database/service name and TLS settings the way each driver
// expects (mysql config, postgres/mssql/oracle URLs). Returns the dsn, the
// driver name to pass to sql.Open, and an error for an unparseable address or
// unsupported driver.
func makeDSN(addr string, o *sqlOptions) (dsn, driver string, err error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("sql: target must be host:port, got %q", addr)
	}
	switch o.Driver {
	case "mysql":
		driver = "mysql"
		cfg := mysqldrv.NewConfig()
		cfg.User, cfg.Passwd = o.Username, o.Password
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(host, port)
		cfg.DBName = o.Database
		cfg.ParseTime = true
		if o.TLSInsecure {
			cfg.TLSConfig = "skip-verify"
		}
		dsn = cfg.FormatDSN()

	case "postgres":
		driver = "postgres"
		ssl := o.SSLMode
		if ssl == "" {
			if o.TLSInsecure {
				ssl = "require"
			} else {
				ssl = "disable"
			}
		}
		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(o.Username, o.Password),
			Host:   net.JoinHostPort(host, port),
			Path:   "/" + o.Database,
		}
		q := u.Query()
		q.Set("sslmode", ssl)
		u.RawQuery = q.Encode()
		dsn = u.String()

	case "mssql":
		driver = "sqlserver"
		u := &url.URL{
			Scheme: "sqlserver",
			User:   url.UserPassword(o.Username, o.Password),
			Host:   net.JoinHostPort(host, port),
		}
		q := u.Query()
		q.Set("database", o.Database)
		if o.TLSInsecure {
			q.Set("TrustServerCertificate", "true")
		}
		u.RawQuery = q.Encode()
		dsn = u.String()

	case "oracle":
		driver = "oracle"
		svc := o.ServiceName
		if svc == "" {
			svc = o.Database
		}
		u := &url.URL{
			Scheme: "oracle",
			User:   url.UserPassword(o.Username, o.Password),
			Host:   net.JoinHostPort(host, port),
			Path:   "/" + svc,
		}
		if o.ServiceName != "" && o.Database != "" {
			q := u.Query()
			q.Set("SID", o.Database)
			u.RawQuery = q.Encode()
		}
		dsn = u.String()

	default:
		return "", "", fmt.Errorf("unsupported driver %q", o.Driver)
	}
	return dsn, driver, nil
}
