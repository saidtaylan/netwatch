# netwatch — Ansible Deployment

Kubespray-style deployment for the **netwatch** distributed network monitoring cluster.

- **Backend**: Go binary + systemd + `CAP_NET_RAW` (ICMP ping without root)
- **Frontend**: Nuxt SPA (pre-built static files) served by **nginx** — no Node.js runtime needed in production
- **Cluster gossip**: Memberlist-based peer discovery, peer list auto-built from inventory

---

## Prerequisites

| Requirement | Details |
|---|---|
| Ansible | ≥ 2.14 (`pip install ansible`) |
| `ansible.posix` collection | `ansible-galaxy collection install ansible.posix` |
| SSH access | Key-based auth to all target hosts |
| sudo / become | Passwordless `sudo` on all targets (or use `--ask-become-pass`) |
| Target OS | Debian/Ubuntu or RedHat/CentOS/Rocky (systemd required) |

---

## Step 1 — Pre-build artifacts

Nothing is compiled on the target servers. You build locally and Ansible copies the binaries.

### Backend binary

```bash
# From the repo root
GOOS=linux GOARCH=amd64 go build -o ansible/files/netwatch ./cmd/linux/
```

### Frontend static files

```bash
# From the frontend/ directory
cd frontend
pnpm install
pnpm build        # output lands at frontend/.output/public/

# Copy static output into the ansible files/ directory
cp -r .output/public/. ../ansible/files/frontend/
```

After this step:
```
ansible/files/
├── netwatch          ← Linux/amd64 binary
└── frontend/
    ├── index.html
    └── _nuxt/
        └── *.js / *.css
```

---

## Step 2 — Configure inventory

```bash
cp inventory/hosts.example.ini inventory/hosts.ini
```

Edit `inventory/hosts.ini` and add your server IPs and SSH user:

```ini
[netwatch_nodes]
node-01 ansible_host=192.168.1.10 ansible_user=ubuntu
node-02 ansible_host=192.168.1.11 ansible_user=ubuntu
node-03 ansible_host=192.168.1.12 ansible_user=ubuntu

[netwatch_frontend]
frontend ansible_host=192.168.1.20 ansible_user=ubuntu
```

> **Note:** `ansible_host` is used to build the gossip peer list. Make sure it
> resolves / is reachable from the other backend nodes.

---

## Step 3 — Configure variables

### `inventory/group_vars/all.yml`

The **most important** variable to change:

```yaml
# MUST be changed. MUST be the same on every node.
# Used for first admin setup and JWT token signing.
netwatch_setup_token: "CHANGE_ME_strong_secret"
```

Other commonly adjusted values:

| Variable | Default | Description |
|---|---|---|
| `netwatch_http_port` | `10240` | Backend REST API port |
| `netwatch_gossip_port` | `7946` | Memberlist gossip port (TCP + UDP) |
| `netwatch_frontend_port` | `80` | nginx listen port |
| `netwatch_version` | `"1.0.0"` | Informational only |

### `inventory/group_vars/netwatch_nodes.yml`

Tune probe behaviour per node group:

| Variable | Default | Description |
|---|---|---|
| `netwatch_probe_interval_sec` | `60` | How often nodes run probes |
| `netwatch_max_retries` | `3` | Retries before marking down |
| `netwatch_timeout` | `5` | Per-probe timeout (seconds) |
| `netwatch_probe_replication_factor` | `3` | Cross-check nodes for failing targets |

---

## Step 4 — Deploy

```bash
ansible-playbook playbooks/setup.yml -i inventory/hosts.ini
```

Deploy only backend nodes:
```bash
ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --tags backend
```

Deploy only frontend:
```bash
ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --tags frontend
```

Dry-run (check mode):
```bash
ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --check --diff
```

---

## Step 5 — First login

1. Open `http://<frontend-ip>/` in your browser
2. You will be prompted to create the first admin account
3. Use the `netwatch_setup_token` you set in `all.yml` to authenticate the setup

---

## Firewall ports

Open the following ports on your firewall / security groups:

| Port | Protocol | Direction | Purpose |
|---|---|---|---|
| `10240` | TCP | node ↔ node, client → node | Backend REST API / health |
| `7946` | TCP + UDP | node ↔ node | Memberlist gossip |
| `80` | TCP | client → frontend | nginx (Nuxt SPA) |

---

## Updating

### Update backend binary

1. Rebuild: `GOOS=linux GOARCH=amd64 go build -o ansible/files/netwatch ./cmd/linux/`
2. Re-run: `ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --tags backend`

The service will only restart if the binary checksum changed.

### Update frontend

1. Rebuild: `cd frontend && pnpm build`
2. Sync output: `cp -r .output/public/. ../ansible/files/frontend/`
3. Re-run: `ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --tags frontend`

nginx reloads gracefully; zero downtime.

### Add a new backend node

1. Add the node to `[netwatch_nodes]` in `inventory/hosts.ini`
2. Re-run the full playbook — all existing nodes get an updated `config.yaml` with
   the new peer in their gossip list, and the new node is fully provisioned.

---

## Directory structure

```
ansible/
├── files/
│   ├── README.md               ← build instructions
│   ├── netwatch                ← pre-built backend binary (you place this)
│   └── frontend/               ← pre-built Nuxt SPA (you place this)
├── inventory/
│   ├── hosts.example.ini       ← copy to hosts.ini and edit
│   └── group_vars/
│       ├── all.yml             ← global vars (ports, token, paths)
│       └── netwatch_nodes.yml  ← backend probe settings
├── playbooks/
│   └── setup.yml               ← main entry point
└── roles/
    ├── netwatch-backend/
    │   ├── tasks/main.yml
    │   ├── handlers/main.yml
    │   └── templates/
    │       ├── config.yaml.j2
    │       └── netwatch-backend.service.j2
    └── netwatch-frontend/
        ├── tasks/main.yml
        ├── handlers/main.yml
        └── templates/
            └── nginx.conf.j2
```

---

## Troubleshooting

**Service fails to start:**
```bash
journalctl -u netwatch-backend -n 100 --no-pager
```

**Health check fails after deploy:**
```bash
curl -v http://<node-ip>:10240/health
```

**Gossip peers not connecting:**
- Verify port `7946` TCP+UDP is open between nodes
- Check `ansible_host` values in inventory match the actual IPs nodes use to reach each other

**nginx 502 / frontend issues:**
```bash
journalctl -u nginx -n 50 --no-pager
nginx -t
```
