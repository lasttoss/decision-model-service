#!/usr/bin/env python3
"""Write eval/cases.jsonl from the table below.

The case set is authored here, by hand, rather than generated from the rules: a set built
out of the rules would be scored at 100% by the rules and would measure nothing. Roughly a
third of the entries carry a reason, and carrying a reason is what marks a case as hard -
it means the obvious keyword reading disagrees with the label, and the evaluation reports
the score on those separately.

    python3 eval/author_cases.py     # writes eval/cases.jsonl
"""
import json
import pathlib

# (input, label) or (input, label, why) - a why means "the obvious reading is wrong here".
CASES = [
    # --- retryable: transient, retrying later is the right action -----------------
    ("rpc error: code = DeadlineExceeded desc = context deadline exceeded", "retryable"),
    ("dial tcp 10.4.2.9:5432: i/o timeout", "retryable"),
    ("read tcp 10.4.2.11:52344->10.4.2.9:6379: connection reset by peer", "retryable"),
    ("connection refused: upstream database not accepting connections yet", "retryable"),
    ("write tcp: broken pipe while streaming snapshot", "retryable"),
    ("upstream returned 503 Service Unavailable", "retryable"),
    ("504 Gateway Timeout from the profile service", "retryable"),
    ("429 Too Many Requests from the search API, retrying after 2s", "retryable"),
    ("connection pool exhausted, waited 30s for a free connection", "retryable"),
    ("etcdserver: leader changed, request may be retried", "retryable"),
    ("net/http: TLS handshake timeout", "retryable"),
    ("stream terminated by RST_STREAM with error code 2", "retryable"),
    ("temporarily unavailable: the shard is being moved", "retryable"),
    ("circuit breaker open for payments, will retry in 5s", "retryable"),
    ("no healthy upstream for the inventory service", "retryable"),
    ("pod evicted from node due to memory pressure, rescheduled", "retryable"),
    ("node ip-10-4-2-7 is NotReady, workload rescheduling", "retryable"),
    ("unexpected EOF while reading the response body", "retryable"),
    ("DNS resolution failed for cache.internal: server misbehaving", "retryable"),
    ("server busy, please try again later", "retryable"),
    ("deadlock detected, transaction rolled back", "retryable",
     "deadlocks look fatal, but PostgreSQL resolves them by rolling one side back and the correct action is to retry"),
    ("failed to acquire lease on shard 3: already held by another leader", "needs_human",
     "this looks transient, but two leaders on one shard is a split-brain symptom, not load"),
    ("timeout after 30s waiting for a free worker slot", "retryable"),
    ("connection reset by peer while the client was still sending", "retryable"),
    ("upstream connect error or disconnect/reset before headers", "retryable"),
    ("retry budget exhausted after 5 attempts: deadline exceeded", "retryable"),
    ("io.grpc.StatusRuntimeException: UNAVAILABLE", "retryable"),
    ("temporary failure in name resolution", "retryable"),
    ("the replica is catching up, reads will be served shortly", "retryable"),
    ("request cancelled because the node drained", "retryable"),
    ("kafka broker not available yet, continuing to retry", "retryable"),
    ("read-only transaction refused: could not serialize access", "retryable"),
    ("socket hang up while uploading the bundle", "retryable"),
    ("the load balancer returned 502 for the whole fleet for 4 seconds", "retryable"),
    ("redis cluster: CLUSTERDOWN, slots are being migrated", "retryable"),
    ("grpc: the connection is draining", "retryable"),
    ("HTTP 408 from the origin server", "retryable"),
    ("failed to send to the message broker: queue full, backing off", "retryable"),
    ("the upstream took longer than the 5s budget, request abandoned", "retryable"),

    # --- expected: normal operation that looks like an error ---------------------
    ("context canceled", "expected"),
    ("context canceled: the client closed the connection before the response", "expected"),
    ("request cancelled by the client", "expected"),
    ("client disconnected while streaming telemetry", "expected"),
    ("client hung up before sending a request", "expected"),
    ("readiness probe succeeded after 1s", "expected"),
    ("liveness probe returned 200", "expected"),
    ("health check ok, serving traffic", "expected"),
    ("graceful shutdown started, draining connections", "expected"),
    ("shutting down: 0 in-flight requests", "expected"),
    ("shutdown signal received, 2 requests still draining", "expected"),
    ("reloaded config from /etc/liveops/config.yaml", "expected"),
    ("reconnect established after 1 attempt", "expected"),
    ("deployment rollout complete: 3 of 3 replicas updated", "expected"),
    ("migration already applied, nothing to do", "expected",
     "the word migration points at needs_human, but a migration that reports nothing to do is the healthy case"),
    ("starting http server on :8080", "expected"),
    ("loaded config: connect_timeout=5s retry_max=3 backoff=200ms", "expected",
     "a configuration dump mentioning timeout and retry is not an incident, and a keyword baseline reads it as one"),
    ("health check passed after 2 retries", "expected",
     "retries that succeed are the mechanism working, not a failure to report"),
    ("cache miss for key session:9f21, populated from origin", "expected"),
    ("feature flag double_xp evaluated to false", "expected"),
    ("session token refreshed for user 4711", "expected"),
    ("worker claimed 0 fires - nothing was pending", "expected"),
    ("audit row written for config.publish", "expected"),
    ("404 for /favicon.ico from the browser warmup", "expected"),
    ("TLS certificate renewed successfully, expires in 89 days", "expected"),
    ("connection pool warmed up: 8 connections established", "expected"),
    ("the queue is empty, idling for 1s", "expected"),
    ("metrics scrape completed in 41ms", "expected"),
    ("leader election won by this node, term 14", "expected",
     "a leader election that ends with a winner is routine; only a lost quorum is an incident"),
    ("backup snapshot uploaded to object storage", "expected"),
    ("user 4711 logged out, session removed", "expected"),
    ("rate limiter allowed the request (token bucket refilled)", "expected"),
    ("upstream health check disabled by configuration", "expected"),
    ("the canary received 5% of traffic as configured", "expected"),
    ("autoscaler added 1 replica, total 4", "expected"),
    ("connection closed by the remote peer after an idle period", "expected"),
    ("received SIGTERM, will finish the current batch", "expected"),
    ("the stream ended because the client stopped watching", "expected",
     "the same words as a truncated stream, and the opposite meaning: only the client's intent tells them apart"),
    ("config hot reload applied without dropping connections", "expected"),
    ("the lock was released by the holder after the work finished", "expected"),
    ("trace sampled at 1%, this request was not recorded", "expected"),
    ("warmup request to the pricing service returned 200", "expected"),
    ("pod started, waiting for the readiness gate", "expected"),
    ("cache eviction removed 120 expired entries", "expected"),

    # --- needs_human: it will not fix itself -------------------------------------
    ("permission denied while opening /var/lib/liveops/state.db", "needs_human"),
    ("unauthorized: token audience does not include this service", "needs_human"),
    ("403 Forbidden from the billing API for project 88", "needs_human"),
    ("invalid credentials for the vault login", "needs_human"),
    ("panic: runtime error: nil pointer dereference", "needs_human"),
    ("panic: assignment to entry in nil map", "needs_human"),
    ("fatal error: unexpected signal during runtime execution (segmentation fault)", "needs_human"),
    ("write /var/lib/postgresql/data: no space left on device", "needs_human"),
    ("checksum mismatch on segment 41, refusing to start", "needs_human"),
    ("invariant violated: balance is negative for account 91", "needs_human"),
    ("schema mismatch: column sessions.channel does not exist", "needs_human"),
    ("pq: no such table users", "needs_human"),
    ("constraint violation: duplicate key value violates unique constraint sessions_pkey", "needs_human"),
    ("monthly quota exceeded for the translation API", "needs_human"),
    ("certificate expired on 2026-09-30, clients are rejecting the handshake", "needs_human"),
    ("x509: certificate signed by unknown authority", "needs_human"),
    ("signature verification failed for the release artifact", "needs_human"),
    ("attempted a write to a read-only replica", "needs_human"),
    ("secret liveops/db-password not found in vault", "needs_human"),
    ("invalid configuration: unknown field replicaCounts (did you mean replicaCount?)", "needs_human"),
    ("migration 0007 failed: relation events already exists", "needs_human"),
    ("data corruption detected in the snapshot index", "needs_human"),
    ("OOMKilled: the container exceeded its memory limit of 256Mi", "needs_human"),
    ("too many open files: the descriptor table is full and it is not recovering", "needs_human",
     "a resource exhaustion that persists is a leak, and retrying keeps the process at the ceiling"),
    ("the service account has no permission to list endpoints", "needs_human"),
    ("timeout talking to the secrets backend: permission denied", "needs_human",
     "a timeout says retry and permission denied says stop; a triage tool that reads only the first word sends it to the wrong queue"),
    ("context canceled: could not reach vault due to invalid credentials", "needs_human",
     "the cancellation is a symptom of the credential failure, not of a client giving up"),
    ("429 from the ledger API: quota exceeded for the month", "needs_human",
     "429 normally means slow down, but a monthly quota means waiting will not help"),
    ("503 from the auth service followed by invalid credentials for 40 minutes", "needs_human",
     "the 503 is over; the credential problem is what is left"),
    ("connection refused to the primary, and the replica promotion never happened", "needs_human",
     "refused suggests a retry, but a failed promotion is a human decision about data"),
    ("the migration plan wants to drop column users.legacy_id", "needs_human"),
    ("replica lag is 4 hours and growing", "needs_human"),
    ("the audit trail rejected an UPDATE, which should be impossible", "needs_human"),
    ("two writers reported the same revision number", "needs_human"),
    ("the disk is 98% full and the retention job is disabled", "needs_human"),
    ("kafka consumer group has no members and the lag is 2 million", "needs_human"),
    ("the feature flag service returned a value that is not in the schema", "needs_human"),
    ("row count mismatch after the nightly aggregation", "needs_human"),
    ("the encryption key was rotated without re-encrypting the existing rows", "needs_human"),
    ("login succeeded for a disabled account", "needs_human"),
    ("the retry queue is 12 hours behind and still growing", "needs_human"),
    ("deployment rolled back automatically after 3 failed readiness checks and stayed rolled back", "needs_human"),
    ("the rate limiter is rejecting 100% of traffic from our own region", "needs_human"),
    ("cluster quorum lost: 2 of 3 nodes are unreachable", "needs_human"),
    ("the same panic has restarted the process 47 times in an hour", "needs_human"),
]

def main():
    out = pathlib.Path(__file__).with_name("cases.jsonl")
    lines = [
        "# The frozen case set. Authored by hand in author_cases.py; regenerate with",
        "# `python3 eval/author_cases.py`. A case with a `why` is a hard case: the obvious",
        "# keyword reading disagrees with the label, and the evaluation scores those",
        "# separately. See eval/README.md for the labelling rule.",
    ]
    hard = 0
    for i, entry in enumerate(CASES, start=1):
        text, label = entry[0], entry[1]
        case = {"id": f"c{i:03d}", "input": text, "label": label}
        if len(entry) > 2:
            case["hard"] = True
            case["why"] = entry[2]
            hard += 1
        lines.append(json.dumps(case, ensure_ascii=False))
    out.write_text("\n".join(lines) + "\n", encoding="utf-8")
    per = {}
    for entry in CASES:
        per[entry[1]] = per.get(entry[1], 0) + 1
    print(f"wrote {out.name}: {len(CASES)} cases {per}, hard {hard}")

if __name__ == "__main__":
    main()
