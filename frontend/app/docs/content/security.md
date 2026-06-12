# Security

Two independent security layers: **authentication** for the API/UI, and **encryption** for the gossip wire.

## API authentication (JWT)

netwatch uses JWT-based, multi-user auth.

- **`admin.setup_token`** — a single secret in `config.yaml`, identical on every node. It does two jobs: it authorizes creating the **first** admin user on the `/setup` page, and it is the **HMAC-SHA256 secret** that signs all JWTs.
- Because every node shares that secret, **a JWT minted by one node is valid on every node** — the frontend can hit any backend with the same token.
- Passwords are hashed with **bcrypt** (cost 12). They are never stored or transmitted in plaintext.
- Tokens have a 24-hour TTL.

### The first-run flow

```
/connect  → enter a backend node URL → GET /auth/status
              │ setup_completed=false              │ setup_completed=true
              ▼                                     ▼
           /setup (setup_token + admin/pw)       /login (username + pw)
              └────────────── JWT ───────────────┘
```

### Users & roles

- An admin creates other users — there is **no public registration page**.
- Roles: `admin` / `operator` / `viewer`. `admin` can manage users; finer enforcement of operator/viewer is evolving.
- The `users` table is gossip-replicated (LWW), so a user created on one node exists cluster-wide. Writes are refused on a node that has lost quorum.

| Endpoint | Auth | Purpose |
|---|---|---|
| `POST /auth/setup` | setup_token | Create the first admin. |
| `POST /auth/login` | none | Get a JWT. |
| `PUT /auth/password` | JWT | Change own password. |
| `POST /auth/reset-password` | setup_token | Recovery reset. |
| `GET /users`, `PUT/DELETE /users/{id}` | admin JWT | User management. |

> If `admin.setup_token` is empty, `/auth/setup` refuses to run and the UI cannot be set up — it is **required**. Keep it long and random, and the same on all nodes.

## Gossip encryption (keyring)

The gossip wire (state, config, storage changes) can be **encrypted and authenticated** with a symmetric keyring:

```yaml
cluster:
  keyring:
    - "base64-encoded-AES-256-key=="   # 16, 24 or 32 raw bytes
```

- Keys are base64-encoded AES-128/192/256, validated at config load.
- The **first** key encrypts outgoing messages; **all** keys are tried for decryption — which is what makes zero-downtime rotation possible.
- If `cluster.enabled: false`, no network ports are opened at all.

### Zero-downtime key rotation

Rotate without dropping a single gossip message, via `POST /cluster/keyring/rotate` (or the UI Keyring page):

```
1. add    the new key everywhere   → all nodes can now DECRYPT with old+new
2. use    the new key everywhere   → all nodes now ENCRYPT with new
3. remove the old key everywhere   → old key retired
```

Because every node accepts all keys during the overlap, there is never a moment where a sender uses a key a receiver doesn't hold. `GET /cluster/keyring/rotate` reports the current key count and the primary key's prefix.

> The keyring is the one shared-config field where **drift is fatal**: if nodes hold different keys with no overlap, they can't decrypt each other's gossip and the cluster splits. [Config sync](config-sync) surfaces keyring drift; always rotate in the add → use → remove order across *all* nodes.
