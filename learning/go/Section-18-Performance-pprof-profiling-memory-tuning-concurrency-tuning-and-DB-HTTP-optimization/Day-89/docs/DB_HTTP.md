# Day 89 — Database and HTTP performance

All numbers from `go run ./Day-89`: a real SQLite file and a real HTTP server on
localhost. **Localhost has microsecond latency, so every ratio here is a floor.**
Across a network they get dramatically worse — which is the point of the
simulated-latency run below.

## 1. EXPLAIN: "we have an index" ≠ "the query uses it"

10,000 orders, 200 lookups by `(customer_id, status)`:

| | plan | seeks? | 200 lookups |
|---|---|---|---|
| no index | `SCAN orders` | no | 87.5 ms |
| `idx_orders_customer_status` | `SEARCH orders USING INDEX … (customer_id=? AND status=?)` | **yes** | **2.2 ms** (40×) |

The word to look for is **SEARCH** (a seek: log n to find the range, then read
only matches) versus **SCAN** (read everything). `SCAN … USING COVERING INDEX`
is a real plan and a real trap: it touches the index, avoids the table, and is
still O(n).

Three ways an index quietly stops working, all measured:

| Query | Plan | What happened |
|---|---|---|
| `customer_id = ? AND UPPER(status) = ?` | `SEARCH … (customer_id=?)` | The `status` half is unusable. Every order of that customer is read and filtered in memory. |
| `DATE(created_at) = ?` | `SCAN … USING COVERING INDEX` | A function on the **leading** column abandons the seek entirely. Store the value you search by, or index the expression. |
| `status = ?` with an index on `(customer_id, status)` | `SCAN … USING COVERING INDEX` | An index is usable **left to right** — a phone book sorted by surname then first name is no help finding every "Ada". |

In each case the query returns the *right answer*, which is why it survives
review. Only `EXPLAIN` says whether it is fast.

## 2. N+1: the cost is the round-trip count

100 customers with their orders, three ways:

**SQLite in-process** (a query costs microseconds):

| strategy | queries | duration |
|---|---|---|
| N+1 | 96 | 2.80 ms |
| one JOIN | 1 | 2.64 ms |
| two queries + `IN (…)` | 2 | 2.20 ms |

Barely a difference — **and that is the trap**. An embedded database has no
round trip to pay for, so N+1 looks free right up until the data moves to
another host.

**The same code with a simulated 0.5 ms round trip** (modest for a database one
hop away):

| strategy | queries | duration | |
|---|---|---|---|
| N+1 | 96 | 62.3 ms | |
| one JOIN | 1 | 3.6 ms | **17.4× faster** |
| two queries + `IN (…)` | 2 | 3.8 ms | 16.5× faster |

96 round trips at half a millisecond is 48 ms of *waiting* in a page that does
almost no work, and it grows linearly with the page size while the CPU idles.

**JOIN or batch?** The JOIN sends the parent columns once per child row. The
two-query version does not, and it still works when the children live in
another database or behind another service — where a JOIN is not available at
all.

## 3. Batching writes

2,000 rows:

| strategy | duration | statements | |
|---|---|---|---|
| one `INSERT` per row | 91 ms | 2,000 | |
| one transaction, prepared once | **6 ms** | 2,000 | **14.5×** |
| multi-row `INSERT`, 500 per statement | 10 ms | **4** | 9.1× |

The first and biggest win is the **transaction**: one commit and one disk sync
instead of 2,000. The second is fewer statements to parse and plan. (Here the
prepared-statement version edges out the multi-row one because SQLite is
in-process; across a network, where each statement is a round trip, the 4-batch
version wins.)

Keep batches **bounded**: SQLite caps at 32,766 parameters, the PostgreSQL wire
protocol at 65,535. `InsertBatched` clamps to 500 rows per statement, and
`TestBatchSizeIsBounded` proves an absurd request gets clamped rather than sent.

## 4. HTTP keep-alive

300 requests to a localhost server, no TLS:

| client | duration | new conns | reused | reuse rate |
|---|---|---|---|---|
| `DisableKeepAlives: true` | 52 ms | 300 | 0 | 0% |
| keep-alive (default) | **11 ms** | **1** | 299 | **100%** |
| keep-alive, body **not drained** | 26 ms | 300 | 0 | 0% |

4.8× on localhost with no TLS. Add a real network and a TLS handshake — two
round trips before any data moves — and the gap is an order of magnitude.

Row three is the one that bites in real code: keep-alive is on, the body is
closed, but it was never **read**, so bytes are still in flight and the
transport cannot return the connection to the pool. **Always drain, then close.**

The other setting people miss: `MaxIdleConnsPerHost` defaults to **2**. A
service making 50 concurrent calls to one host keeps two connections and
re-handshakes the other 48 every time. `httptrace.GotConn` reports `Reused` per
request, which is how you check instead of guessing.

## 5. Timeouts everywhere

`http.DefaultClient` has **no timeout at all**. A hung dependency holds the
goroutine, its stack and its connection until the TCP stack gives up — minutes.
Enough of those and the service is out of workers while every dashboard shows
an idle CPU.

Measured against a server that accepts the connection and never replies:

| | result |
|---|---|
| `ResponseHeaderTimeout = 200 ms` | failed after **201 ms** |
| caller's `context` deadline = 50 ms | failed after **51 ms**, `errors.Is(err, context.DeadlineExceeded)` |

Four layers, because they are not one knob:

| Layer | Suggested | Catches |
|---|---|---|
| `Dialer.Timeout` | 2 s | the host is down — fail fast |
| `TLSHandshakeTimeout` | 2 s | the certificate exchange stalls |
| `ResponseHeaderTimeout` | 3 s | **the server accepted and is thinking** — the one that catches hangs |
| `Client.Timeout` | 10 s | everything, *including reading the body* |

A single `Client.Timeout` also caps a legitimate five-minute download, which is
why streaming endpoints need the layered version. And the caller's context
deadline must override all of them when it is shorter — it is the only one that
knows how long the *user* is willing to wait.

## The through-line

Everything on this page is the same lesson: **most slowness is waiting, not
computing.** A full scan waits on disk, N+1 waits on the network 96 times, an
unbatched import waits on 2,000 disk syncs, a fresh TCP connection waits on a
handshake, and a missing timeout waits forever. Profile the CPU all you like —
none of it shows up there.
