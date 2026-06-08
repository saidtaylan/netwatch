#!/usr/bin/env python3
"""Delete e2e test users from the live backend."""
import json
import urllib.request

BACKEND = "http://localhost:10241"
ADMIN = {"username": "testadmin", "password": "AdminPass123!"}
TEST_USERS = {"e2e-test-viewer", "e2e-test-operator"}

# Login
req = urllib.request.Request(
    f"{BACKEND}/auth/login",
    data=json.dumps(ADMIN).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)
with urllib.request.urlopen(req) as r:
    token = json.loads(r.read())["token"]

# List users
req2 = urllib.request.Request(
    f"{BACKEND}/users",
    headers={"Authorization": f"Bearer {token}"},
)
with urllib.request.urlopen(req2) as r:
    users = json.loads(r.read())

# Delete matching users
for u in users:
    if u["username"] in TEST_USERS:
        del_req = urllib.request.Request(
            f"{BACKEND}/users/{u['id']}",
            headers={"Authorization": f"Bearer {token}"},
            method="DELETE",
        )
        try:
            with urllib.request.urlopen(del_req) as dr:
                print(f"Deleted {u['username']}: {dr.status}")
        except urllib.error.HTTPError as e:
            print(f"Error deleting {u['username']}: {e.code} {e.read()}")

print("Done.")
