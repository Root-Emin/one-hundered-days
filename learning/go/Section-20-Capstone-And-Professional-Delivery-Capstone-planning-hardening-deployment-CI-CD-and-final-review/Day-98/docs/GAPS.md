# Linkr — gaps

What is missing, with **why not yet** and **what would trigger it**. The second
column is what stops this being a wish nobody reads.

## Security

| Gap | Why not yet | Trigger |
|---|---|---|
| No key revocation command | Issuing works; revoking is a `DELETE` nobody has written a command for. | The first key that leaks. |
| No audit log | Owner is recorded, but not who did what and when. | A customer asking "who deleted this link?" |
| No abuse detection | Nothing stops a valid key shortening links to malware. A real shortener needs a blocklist and a report endpoint. | Public exposure. This is the item that would matter first if Linkr were real. |
| No TLS here | It speaks HTTP behind a proxy. | Running without one — and then it needs a design, not a flag. |

## Reliability

| Gap | Why not yet | Trigger |
|---|---|---|
| One instance, one SQLite file | ADR 0001: the operational cost of a database server is not worth it at this size. | The redirect rate exceeding one machine, or availability needing a second instance. |
| The redirect cache is per-process | A second replica would have a second, independently stale cache. | The moment there are two replicas. |
| No dead-letter queue for click events | A poison event is counted and left in place with an `attempts` count; the queue is not blocked, but nobody is paged. | The first event that fails repeatedly. |
| Clicks can be lost in the crash window | The event is written after the response, and a crash in that window loses it. Counting is not worth the redirect's latency ([ADR 0003](adr/0003-async-clicks.md)). | A billing feature depending on click counts. |

## Product

| Gap | Why not yet | Trigger |
|---|---|---|
| No referrer breakdown | Story 7, the only "could" in the requirements, cut first as planned. | Someone asking for it. |
| No custom domains | One namespace keeps code generation and routing simple. | A customer who pays. |
| No link editing | A link's target is immutable: changing it silently changes what a shared link does, which is a phishing primitive. | Never, without a very good argument. |

## Testing and tooling

| Gap | Why not yet | Trigger |
|---|---|---|
| No test at 10,000 links | The load test hammers one hot code, which is the realistic shape. | Before promising anything about cold-cache latency. |
| Go 1.26.5 has 5 known stdlib vulnerabilities | Fixed in 1.26.6; see [SECURITY.md](SECURITY.md). | **Now** — this is the one open item with a deadline. |

## The honest summary

The service does what the requirements said, with the numbers measured rather
than assumed. What is missing is mostly what a **real** shortener needs and a
capstone does not: abuse handling, key rotation, and more than one instance.

The one item with a date on it is the toolchain upgrade.
