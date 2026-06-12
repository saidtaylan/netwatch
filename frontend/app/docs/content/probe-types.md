# Probe types reference

Each target has a `type` and a type-specific `options` object. `options` is stored as **raw JSON** and parsed by that type's checker, so rich, type-specific options are never flattened away. Every checker validates its options at config-load time and rejects unknown fields — a typo fails startup, not a 3 a.m. probe.

## tcp

A successful TCP connect within `timeout` = up. No options.

```yaml
- id: "pg"
  type: tcp
  target: "db.internal:5432"
```

## http / https

The target is a full URL. Up = an accepted status (default: any `< 400`) and, optionally, body assertions pass.

```yaml
- id: "api-health"
  type: http
  target: "https://api.internal/health"
  options:
    method: "GET"                 # GET POST PUT DELETE HEAD OPTIONS PATCH
    headers: { Authorization: "Bearer ${API_TOKEN}" }
    body: ""                      # optional request body
    expected_status: { in: [200, 204] }
    body_contains: "\"status\":\"ok\""
    body_not_contains: "error"
    follow_redirects: true
    max_redirects: 5
```

| Option | Meaning |
|---|---|
| `method` | HTTP method (default GET). |
| `headers` | Request headers (supports `${VAR}` injection). |
| `body` | Request body string. |
| `expected_status` | A status rule — **exactly one** operator: `in: [...]`, `lt`, `lte`, `gt`, `gte`, or `between: [min,max]`. Omit to accept any `< 400`. |
| `body_contains` | Substring that must appear in the response body. |
| `body_not_contains` | Substring that must **not** appear. |
| `follow_redirects` | Whether to follow 3xx (default follows). |
| `max_redirects` | Redirect hop limit (`>= 0`). |

Body assertions are read from up to 1 MiB of the response and are not allowed with `HEAD` (no body).

## ping (ICMP)

```yaml
- id: "gw"
  type: ping
  target: "192.168.1.1"          # hostname or IPv4
```

One ICMPv4 echo; up = a matching echo reply within the deadline. **Requires `CAP_NET_RAW`** on Linux (granted by the systemd unit and the container images), or root / Administrator. IPv4 only. No options.

## dns

The target is the hostname to resolve. Up = it resolves (and, if `expected_ips` is set, to one of those addresses).

```yaml
- id: "zone-a"
  type: dns
  target: "example.com"
  options:
    expected_ips: ["93.184.216.34"]   # any match → up; omit to accept any resolution
    nameserver: "8.8.8.8"             # or "8.8.8.8:53"; omit to use the system resolver
```

| Option | Meaning |
|---|---|
| `expected_ips` | Acceptable resolved addresses; any match is up. Each must be a valid IP. |
| `nameserver` | A specific resolver (IP or IP:port) instead of the system one. |

## sql

The target is `host:port`. Up = a connection succeeds and an optional liveness query runs without error. The DSN is **built for you** from the options — you never write a raw connection string.

```yaml
- id: "warehouse"
  type: sql
  target: "warehouse.internal:1521"
  options:
    driver: "oracle"             # oracle | mysql | postgres | mssql
    username: "monitor"
    password: "${ORA_PASS}"
    service_name: "ORCL"         # oracle: service_name OR database (SID)
    query: "SELECT 1 FROM dual"  # optional; omit to just ping
```

| Option | Meaning |
|---|---|
| `driver` | `oracle`, `mysql`, `postgres`, or `mssql`. |
| `username` / `password` | Credentials (use `${VAR}` for the password). |
| `database` | Database name. Required for mysql/postgres/mssql; for Oracle it is the SID (or use `service_name`). |
| `service_name` | Oracle only — the service name. |
| `query` | A query that must succeed (no-rows is OK). Omit to just open + ping. |
| `ssl_mode` | Postgres only: `disable` / `require` / `verify-ca` / `verify-full`. |
| `tls_insecure` | mysql / postgres / mssql: skip certificate verification. |

## Credential injection

Anywhere in `options` you can reference `${VAR}`, resolved from the `credentials_file`:

```yaml
credentials_file: "/etc/netwatch/credentials.env"
```

```
ORA_PASS=supersecret
API_TOKEN=abc123
```

Unresolved `${VAR}` references fail config validation, so a missing secret is caught at startup, not at probe time.
