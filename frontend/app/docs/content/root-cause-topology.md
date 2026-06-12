# Root cause & dependency topology

When a database dies, every service that depends on it also fails. Most tools fire an alert for each one — a wall of noise where the actual cause is buried. netwatch lets you declare dependencies and performs **root-cause analysis**, so the alert tells you *why*, not just *what*.

## Declaring dependencies

Add `depends_on` to a target, listing the ids of the targets it relies on:

```yaml
targets:
  - id: "db-primary"
    type: tcp
    target: "10.0.0.5:5432"
  - id: "api-gateway"
    type: http
    target: "https://api.internal/health"
    depends_on: ["db-primary"]
  - id: "checkout"
    type: http
    target: "https://checkout.internal/health"
    depends_on: ["api-gateway"]
```

The engine builds a **dependency graph** from these edges. Cyclic references and unknown ids are rejected at config load — the graph is always a DAG.

## Root-cause analysis

When `checkout` goes down, `FindRootCause` walks the dependency chain to the **deepest target that is itself down**:

- `checkout` depends on `api-gateway`, which depends on `db-primary`.
- If all three are down, the root cause is `db-primary` — the leaf of the failure.
- The alert for `checkout` carries `ROOT_CAUSE=db-primary` and `DEPENDENCY_DEPTH=2` (two hops from the root).

This works **even across nodes**: root cause is resolved against the gossip-merged, cluster-wide view of state, so the fact that `db-primary` is probed by a different node than `checkout` doesn't matter — `checkout`'s prober still sees `db-primary`'s `hard_down` via gossip.

## Cascading impact

The graph is also traversed the other way. For a target that is down, `CascadingImpact` lists every target that (transitively) depends on it — the blast radius:

- `db-primary` down → `CASCADING_IMPACT=api-gateway,checkout`.

This is attached to the root cause's alert, so the one page you get says both *what failed* and *what it takes down*.

## In alerts

For a down target with `depends_on` configured:

| Variable | Meaning |
|---|---|
| `ROOT_CAUSE` | The deepest down dependency. |
| `DEPENDENCY_DEPTH` | Hops from the root cause (`0` = this is the root). |
| `CASCADING_IMPACT` | Targets that depend on this one. |

A common alerting pattern: only page on alerts where `DEPENDENCY_DEPTH = 0` (the root cause), and treat the rest as informational — turning twenty symptom alerts into one actionable page.

## Where you see it

- **API** — `GET /topology` returns each target's direct deps, reverse deps, and transitive cascading impact.
- **UI** — the Topology page renders the graph: root/independent targets, a depends-on / cascades-to table, and live up/down state.
