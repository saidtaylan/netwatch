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
| SSH access | Key-based auth to all target hosts |
| sudo / become | Passwordless `sudo` on all targets (or use `--ask-become-pass`) |
| Target OS | Debian/Ubuntu or RedHat/CentOS/Rocky (systemd required) |
| Internet on targets | Default mode downloads release artifacts from github.com on each host |

No Go, Node.js, or rsync is needed — neither on the controller nor the targets.

---

## Step 1 — Choose what to install (no build required)

By default Ansible **downloads prebuilt artifacts from the GitHub Releases page** —
the backend binary (arch-matched: amd64/arm64) and the frontend `tar.gz` — and
installs them. Nothing is compiled anywhere.

Pin the version (or track `latest`) in `inventory/group_vars/all.yml`:

```yaml
netwatch_install_from: "release"   # default
netwatch_version: "latest"         # or a tag, e.g. "v0.1.3"
netwatch_repo_slug: "saidtaylan/netwatch"
```

<details>
<summary><b>Optional: install from a local build instead</b></summary>

If you'd rather ship your own build (air-gapped networks, forks, unreleased
changes), set `netwatch_install_from: "local"` and place the artifacts yourself:

```bash
# Backend (set GOARCH=arm64 for arm targets)
cd backend && make build-linux && cp bin/netwatch-linux-amd64 ../ansible/files/netwatch

# Frontend
cd ../frontend && pnpm install && pnpm build
cp -r .output/public/. ../ansible/files/frontend/
```
</details>

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

### Update to a new release

1. Bump `netwatch_version` in `inventory/group_vars/all.yml` (or keep `latest`).
2. Backend: `ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --tags backend`
   (the service restarts only when the binary actually changes).
3. Frontend: `ansible-playbook playbooks/setup.yml -i inventory/hosts.ini --tags frontend`
   (nginx reloads gracefully; zero downtime).

> Local-build mode (`netwatch_install_from: local`): rebuild and refresh
> `ansible/files/` as in Step 1, then re-run the same commands.

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
