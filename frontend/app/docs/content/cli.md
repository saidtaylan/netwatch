# CLI reference

The `netwatch` binary is both the agent and a small set of lifecycle subcommands. With no subcommand it starts the agent.

```bash
netwatch [--config FILE]          # start the monitoring agent (default)
```

## Cluster bootstrap

| Command | Description |
|---|---|
| `netwatch init [--cluster] [--bind-port N] [--force]` | Generate a config skeleton. With `--cluster` it also generates a random AES-256 keyring and prints a copy-paste `join` command for other nodes. |
| `netwatch join --keyring K --addr H:P [--config PATH] [--bind-port N] [--node-name N]` | One-command join: writes a config with `cluster.enabled=true` and the given gossip seed, preserving the rest of an existing config. |
| `netwatch keyring generate` | Print a fresh base64 AES-256 key (for rotation or manual setup). |

When a cluster-enabled agent starts, it prints a startup banner with the node's details and a ready-to-share `join` command.

## Lifecycle

| Command | Description |
|---|---|
| `netwatch validate [--config FILE]` | Validate the config without starting. Reports targets, channels, cluster and SLO status, and fails on errors (unknown deps, bad keyring, unresolved `${VAR}`, out-of-range values). |
| `netwatch leave [--port PORT]` | Tell a running agent to gracefully leave the cluster. |
| `netwatch uninstall` | Stop the service, remove the unit, optionally delete config. |

## Windows service

| Command | Description |
|---|---|
| `netwatch service install` | Register the Windows Service. |
| `netwatch service remove` | Unregister it. |

## HTTP equivalents

Some lifecycle actions are also available over the API (so the UI can drive them):

| CLI | HTTP |
|---|---|
| `netwatch leave` | `POST /cluster/leave` |
| `netwatch keyring generate` / rotate | `GET` / `POST /cluster/keyring/rotate` |
| `netwatch validate` | (build-time / startup only) |

## Typical first-cluster workflow

```bash
# On the first node:
netwatch init --cluster              # prints a keyring + a `join` command
netwatch                             # start it; banner repeats the join command

# On each additional node, paste the printed command:
netwatch join --keyring <KEY> --addr <first-node-ip>:7946
netwatch
```

After that, add targets through the UI or `PUT /targets/{id}` — they replicate to the whole cluster automatically.
