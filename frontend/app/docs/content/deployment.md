# Deployment

netwatch ships **prebuilt artifacts** on every release — backend binaries (amd64 + arm64), a frontend static bundle, and a multi-arch container image. None of the install methods compile on the target; building from source is an opt-in alternative.

> The backend is a single static binary; the frontend is static files served by **nginx** (no Node.js runtime in production).

## systemd (recommended for Linux)

```bash
git clone https://github.com/saidtaylan/netwatch.git && cd netwatch
sudo apt-get install -y nginx          # or dnf
cd deploy-systemd && sudo ./install.sh # downloads the latest release
```

`install.sh` downloads the arch-matched binary and the UI bundle from the [Releases page](https://github.com/saidtaylan/netwatch/releases), installs a minimal `config.yaml`, sets up the systemd unit and the nginx site. Flags:

- `--version vX.Y.Z` — pin a release.
- `--from-source` — use locally built artifacts instead (`make build-linux` + `pnpm build`).

Then edit `/etc/netwatch/config.yaml` (set `admin.setup_token`, enable cluster, add peers) and `systemctl restart netwatch-backend`.

## Ansible (multi-node)

Fill `ansible/inventory/hosts.ini` with your `[netwatch_nodes]` and a `[netwatch_frontend]` host, then:

```bash
ansible-playbook playbooks/setup.yml -i inventory/hosts.ini
```

By default it **downloads** the binary and frontend bundle from the release on each host (no build, no rsync). The gossip `peers:` list is auto-built from the inventory, so adding a node and re-running the playbook wires it into the cluster. Set `netwatch_install_from: local` to ship your own build instead.

## Docker

```bash
docker run -d --name netwatch -p 10240:10240 -p 7946:7946 --cap-add NET_RAW \
  -v /etc/netwatch/config.yaml:/etc/netwatch/config.yaml:ro \
  -v netwatch-state:/var/lib/netwatch \
  ghcr.io/saidtaylan/netwatch:latest
```

`--cap-add NET_RAW` is only needed for ICMP `ping` targets. The image is multi-arch (amd64 + arm64).

## Kubernetes (Helm)

A DaemonSet chart lives under `backend/helm/netwatch` — one agent per node, `hostNetwork: true` (so gossip and ICMP work), a headless service for peer discovery, and the keyring as a Secret.

```bash
helm install nw backend/helm/netwatch -f my-values.yaml
```

Put your targets, cluster settings and keyring in a values file. The chart pulls `ghcr.io/saidtaylan/netwatch`; resource names derive from the release name (rename-safe).

## Choosing a model

| Method | Best for |
|---|---|
| **Docker** | A single node, a quick trial. |
| **systemd** | One node or a small hand-managed cluster on Linux VMs. |
| **Ansible** | A real multi-node cluster on VMs — peers auto-wired from inventory. |
| **Helm** | Kubernetes — one agent per node as a DaemonSet. |

## Ports & firewall

Open `10240/tcp` (API) between the UI and nodes, and `7946/tcp`+`7946/udp` (gossip) **between all nodes**. ICMP probes need `CAP_NET_RAW` (granted by the systemd unit and the container).
