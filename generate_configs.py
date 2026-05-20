import os
import json

base_dir = "test_cluster"
os.makedirs(base_dir, exist_ok=True)

keyring = ["N9VySoOC7n3BJW3Y2dXeMW5SZM5o0vEtzA4sOkSZ+Ok="]

targets = [
    {
        "id": "db-primary",
        "name": "Primary Database",
        "type": "http",
        "target": "http://127.0.0.1:9999/db-primary",
        "options": {"expected_status": {"in": [200]}}
    },
    {
        "id": "api-gateway",
        "name": "API Gateway",
        "type": "http",
        "target": "http://127.0.0.1:9999/api-gateway",
        "depends_on": ["db-primary"],
        "options": {"expected_status": {"in": [200]}}
    },
    {
        "id": "checkout",
        "name": "Checkout Service",
        "type": "http",
        "target": "http://127.0.0.1:9999/checkout",
        "depends_on": ["api-gateway"],
        "options": {"expected_status": {"in": [200]}}
    },
    {
        "id": "standalone-target",
        "name": "Standalone Target",
        "type": "http",
        "target": "http://127.0.0.1:9999/standalone-target",
        "probe_from": ["node-10", "node-11"],
        "options": {"expected_status": {"in": [200]}}
    }
]

apps = [
    {
        "name": "payment-gateway",
        "owner_team": "fintech-sre",
        "uses": ["db-primary", "api-gateway", "checkout"]
    }
]

slo = {
    "enabled": True,
    "retention_days": 90,
    "targets": [
        {
            "id": "db-primary",
            "target_uptime": 0.99,
            "window": "24h"
        }
    ]
}

notifications = {
    "webhook-alert": {
        "type": "webhook",
        "parameters": {
            "url": "http://127.0.0.1:9999/webhook"
        }
    }
}

for i in range(1, 21):
    node_name = f"node-{i:02d}"
    node_dir = os.path.join(base_dir, node_name)
    os.makedirs(node_dir, exist_ok=True)
    
    port = 40000 + i
    gossip_port = 50000 + i
    
    config = {
        "port": str(port),
        "state_file": os.path.join(node_dir, "state.json"),
        "timeout": 2,
        "max_retries": 1,
        "retry_interval_sec": 5,
        "probe_interval_sec": 10,
        "reload_interval_sec": 5,
        "admin": {
            "token": "test_token"
        },
        "notifications": notifications,
        "default_notify": ["webhook-alert"],
        "cluster": {
            "enabled": True,
            "node_name": node_name,
            "bind_port": gossip_port,
            "peers": [] if i == 1 else ["127.0.0.1:50001"],
            "keyring": keyring,
            "expected_node_count": 20,
            "min_quorum_ratio": 0.5,
            "probe_replication_factor": 3,
            "region": "eu-central" if i <= 10 else "us-east"
        },
        "targets": targets,
        "apps": apps,
        "slo": slo
    }
        
    with open(os.path.join(node_dir, "config.yaml"), "w") as f:
        json.dump(config, f, indent=2)
