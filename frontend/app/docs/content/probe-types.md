# Probe types reference

Each target has a `type` and a type-specific `options` object. Crucially, `options` is stored as **raw JSON** and parsed by each checker — so rich, type-specific options (HTTP body assertions, DNS expected IPs, SQL queries) are never flattened away.

## tcp

A successful TCP connect within `timeout` = up. No options required.

```yaml
- id: "pg"
  type: tcp
  target: "db.internal:5432"
```

## http / https

```yaml
- id: "api-health"
  type: http
  target: "https://api.internal/health"
  options:
    method: "GET"
    expected_status:
      in: [200, 204]            # accept any of these
    body_contains: "\"status\":\"ok\""
    follow_redirects: true
    headers:
      Authorization: "Bearer ${API_TOKEN}"
    timeout_sec: 10
    tls_insecure: false
```

| Option | Meaning |
|---|---|
| `method` | HTTP method (default GET). |
| `expected_status` | `{ in: [...] }` set of acceptable codes, or a single code. |
| `body_contains` | Substring that must appear in the response body. |
| `follow_redirects` | Whether to follow 3xx. |
| `headers` | Map of request headers (supports `${VAR}` injection). |
| `timeout_sec` | Per-request timeout override. |
| `tls_insecure` | Skip certificate verification. |

## ping (ICMP)

```yaml
- id: "gw"
  type: ping
  target: "192.168.1.1"
```

ICMP echo. **Requires `CAP_NET_RAW`** on Linux (the systemd unit and container images grant it) or root/Administrator elsewhere.

## dns

```yaml
- id: "zone-a"
  type: dns
  target: "example.com"
  options:
    resolve: "A"                  # record type
    expected_ips: ["93.184.216.34"]
    server: "8.8.8.8:53"          # optional resolver
```

| Option | Meaning |
|---|---|
| `resolve` | Record type (A/AAAA/CNAME/MX/TXT/…). |
| `expected_ips` | The resolution must return these. |
| `server` | Query a specific resolver instead of the system one. |

## sql

```yaml
- id: "warehouse"
  type: sql
  target: "warehouse.internal:1521"
  options:
    driver: "oracle"              # oracle | mysql | postgres | mssql
    dsn: "user/${ORA_PASS}@//warehouse.internal:1521/ORCL"
    query: "SELECT 1 FROM dual"
```

| Option | Meaning |
|---|---|
| `driver` | `oracle`, `mysql`, `postgres`, or `mssql`. |
| `dsn` | Driver-specific connection string (use `${VAR}` for secrets). |
| `query` | A query that must succeed; defaults to a trivial liveness query per driver. |

## Credential injection

Anywhere in `options` you can reference `${VAR}`, resolved from the `credentials_file`:

```yaml
credentials_file: "/etc/netwatch/credentials.env"
```

```
# credentials.env
ORA_PASS=supersecret
API_TOKEN=abc123
```

Unresolved `${VAR}` references fail config validation, so a missing secret is caught at startup rather than at probe time.
