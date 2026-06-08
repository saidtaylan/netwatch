# ansible/files — Pre-built Artifacts

Place your pre-built artifacts here **before** running the Ansible playbook.
Ansible copies them to the remote servers; nothing is compiled on the target hosts.

---

## 1. Backend binary — `files/netwatch`

The Linux/amd64 Go binary for the netwatch monitoring agent.

**Build command** (run from the repo root):

```bash
GOOS=linux GOARCH=amd64 go build -o ansible/files/netwatch ./cmd/linux/
```

> The binary must be compiled for the target architecture. If your build machine
> is macOS or Windows, the cross-compilation flags above are required.

---

## 2. Frontend static files — `files/frontend/`

The pre-built Nuxt SPA output. Only the **static** `public/` directory is needed
— no Node.js runtime is required on the server (nginx serves it directly).

**Build command** (run from the `frontend/` directory):

```bash
cd frontend
pnpm install      # if not already installed
pnpm build        # generates .output/public/
```

Then copy the static output here:

```bash
cp -r frontend/.output/public/. ansible/files/frontend/
```

After this step the directory should look like:

```
ansible/files/frontend/
├── index.html
├── _nuxt/
│   ├── *.js
│   └── *.css
└── ...
```

---

## Directory layout after populating

```
ansible/files/
├── README.md          ← this file
├── netwatch           ← Linux/amd64 Go binary
└── frontend/
    ├── index.html
    └── _nuxt/
        └── ...
```
