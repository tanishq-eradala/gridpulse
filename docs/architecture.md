# Architecture and next steps

```mermaid
flowchart LR
  SIM[Synthetic simulator] --> API[Go ingestion API]
  API --> DB[(SQLite readings and alerts)]
  API --> SSE[Server-Sent Events]
  SSE --> UI[React console]
  DB --> UI
  API --> METRICS[/metrics]
```

## Alert lifecycle

For each accepted reading, the service loads the three most recent readings for that equipment. A condition becomes active only if all three breach the same threshold. Additional abnormal readings update its last-seen time without opening duplicate alerts. A normal reading resolves the active condition. Every transition is stored in SQLite, and new readings with alert changes are published to connected clients.

Thresholds are educational defaults: temperature above 85 °C, voltage more than 10% from nominal, and efficiency below 92%. They are not equipment specifications. A production system would derive thresholds from validated operating envelopes and engineering review.

## Reliability decisions

- `sampleId` is unique, so a repeated identical reading does not increment counts or publish a second event.
- HTTP bodies have a 1 MiB limit and reject unknown fields.
- SSE clients use bounded channels; slow clients may miss a live event and recover on the UI's 10-second refresh.
- The simulator has a stable run ID and increasing sample sequence for traceability.
- The API uses one SQLite connection to serialize local writes. At larger scale, move ingestion to a durable queue and a time-series or partitioned relational store.

## Next depth milestones

1. Add a replay command that ingests a versioned CSV fixture with deterministic expected alerts.
2. Add request duration, ingestion latency, and dropped-SSE-event metrics with OpenTelemetry traces and Grafana panels.
3. Add an alert acknowledgment and operator note workflow, with audit history.
4. Add a benchmark for sustained ingest and a documented capacity limit.
5. Deploy with authentication and role-based access, then exercise incident recovery and data retention.

Do not describe this as a live transformer monitoring system. The measurements and hardware profile are synthetic.
