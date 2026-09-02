# Runbook — Orders service

The point of a runbook is that a tired person at 3am does not have to think.
Each section is a symptom, then steps, in order.

## Before anything: the three questions

1. **Is it us?** `sum(rate(day75_http_requests_total{status=~"5xx"}[5m])) / sum(rate(day75_http_requests_total[5m]))`
2. **Since when?** Look for the step change on the dashboard, then check what
   deployed at that time.
3. **Is it all of it, or one thing?** Group the error rate by route, then by
   dependency.

Write what you find in the incident channel as you go. The person who joins in
ten minutes needs the trail more than they need your conclusion.

---

## Symptom: error rate spike (5xx > 5%)

1. **Check the breaker.**
   ```
   curl -s $HOST/readyz
   curl -s $HOST/metrics | grep day75_dependency_breaker_state
   ```
   `breaker_state == 2` (open) means the service has already protected itself:
   it is failing fast rather than piling up. The problem is the dependency.

2. **Identify which dependency.**
   ```
   sum by (dependency, outcome) (rate(day75_dependency_calls_total[5m]))
   ```
   `outcome="breaker_open"` means "we are not even trying"; `outcome="error"`
   means "we tried and it failed".

3. **Get a trace id from a failing request** — every error log line carries
   `trace_id`, and every response carries `X-Trace-Id`:
   ```
   curl -si $HOST/orders -d '{"customer":"probe","cents":100}' | grep -i x-trace-id
   ```
   Open that trace. It shows which span failed and how long the others took.

4. **Read the logs for that trace:**
   ```
   {service="day75"} |= "<trace_id>"
   ```

5. **Decide:**
   - Dependency is down → escalate to its owner. Our service is already
     degrading gracefully; do not "fix" it by disabling the breaker.
   - Dependency is slow but alive → consider raising the per-attempt timeout
     *temporarily*, and only if the caller's own deadline allows it.
   - It is us → check the last deploy; roll back rather than debug forward.

6. **If customers are affected**, say so in the status page before you finish
   diagnosing. The business impact panel (`orders_processed_total{outcome=...}`)
   is the number to quote.

---

## Symptom: latency up, errors flat

Usually a slow dependency, not a broken one.

1. `histogram_quantile(0.95, sum by (le, dependency) (rate(day75_dependency_duration_seconds_bucket[5m])))`
2. Compare with the retry rate: if retries are up, the timeouts are firing and
   the extra latency is our own retry loop adding delay on top of a slow
   dependency.
3. Check saturation: `day75_http_requests_in_flight`. If it is climbing, the
   service is queueing and will start timing out on its own soon.
4. Mitigations, in order of preference:
   - Reduce the per-attempt timeout so the retry budget is not spent waiting.
   - Reduce `MaxAttempts` to 1 temporarily: under a slow dependency, retries
     add load without adding success.
   - Shed load at the edge before the queue does it for you.

---

## Symptom: the breaker keeps flapping (open → half-open → open)

The dependency is partially recovered. This is the breaker doing its job, not
a bug.

- Extend the cooldown so probes are less frequent.
- Check whether the probe itself is the problem: if every instance probes at
  once, a fragile dependency is knocked down again. `HalfOpenProbes: 1` per
  instance, and consider jittering the cooldown across instances.
- Do not disable the breaker to "let traffic through". That is how a partial
  outage becomes a total one.

---

## Symptom: no traffic at all

1. Is the process alive? `curl $HOST/healthz` — this endpoint touches no
   dependency on purpose, so it answers even during a database outage.
2. Is it ready? `curl $HOST/readyz` — a 503 here means the load balancer has
   correctly stopped sending traffic.
3. Is it upstream? Check the load balancer and DNS before touching the
   service.

---

## Things that are NOT emergencies

- A single 5xx. Look at the ratio.
- The breaker opening for thirty seconds and closing again — that is the
  design working.
- Retries in the logs during a deploy of the dependency.

---

## After the incident

- Which panel would have shown this earlier? Add it.
- Which log line was missing when you needed it? Add it — with the trace id.
- Was there an alert? If it fired late, tune the threshold. If it did not fire
  at all, write it.
- Was this runbook right? Fix it now, while you still remember.
