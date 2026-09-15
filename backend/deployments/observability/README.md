# Pharmaciano ERP — Production Observability Stack

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          Application Layer                              │
│  ┌──────────┐  ┌──────────┐                                            │
│  │   API    │  │  Worker  │  ← /metrics (Prometheus), structured logs  │
│  └────┬─────┘  └────┬─────┘                                            │
│       │              │                                                  │
│  ┌────┴──────────────┴────┐                                            │
│  │    Middleware Audit     │                                            │
│  │  ┌──────────────────┐  │                                            │
│  │  │ FanoutAuditSink   │  │                                            │
│  │  │ ├─ PostgresSink   │  │──→ PostgreSQL audit_logs (authority)      │
│  │  │ └─ LokiSink       │  │──→ Loki → Grafana Audit Dashboard         │
│  │  └──────────────────┘  │                                            │
│  └─────────────────────────┘                                            │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                        Observability Layer                              │
│                                                                         │
│  Prometheus ──scrape──→ API, Worker, Redis-Exp, PG-Exp, Node-Exp, Loki │
│       │                                                                │
│       └──→ AlertManager ( PagerDuty / Slack / Email )                  │
│                                                                         │
│  Grafana ──→ Prometheus (metrics) + Loki (logs/audit)                  │
│       │                                                                │
│       └── Dashboards: API, Auth, DB, Redis, Infra, Business,          │
│                       Background, Audit Trail                           │
│                                                                         │
│  Promtail ──scrape Docker stdout──→ Loki                               │
│  Loki ←── audit entries (LokiAuditSink) + Promtail (container logs)    │
└─────────────────────────────────────────────────────────────────────────┘
```

## Quick Start

```bash
# Start the full stack
docker compose up -d

# Start with dev tools (adminer, asynqmon)
docker compose --profile dev up -d
```

## Service URLs

| Service        | URL                       | Purpose                          |
|----------------|---------------------------|----------------------------------|
| API            | http://localhost:8080      | Application API                  |
| Grafana        | http://localhost:3000      | Dashboards & exploration         |
| Prometheus     | http://localhost:9090      | Metrics & alerting rules         |
| AlertManager   | http://localhost:9093      | Alert routing & silencing        |
| Loki           | http://localhost:3100      | Log & audit query API            |
| Node Exporter  | http://localhost:9100/metrics | Host metrics                 |
| Redis Exporter | http://localhost:9121/metrics | Redis metrics                |
| PG Exporter    | http://localhost:9187/metrics | PostgreSQL metrics            |
| Adminer*       | http://localhost:8081      | DB management (dev profile)      |
| Asynqmon*      | http://localhost:8082      | Job queue viewer (dev profile)   |

*Dev profile only: `docker compose --profile dev up -d`

## Grafana Dashboards

After startup, Grafana auto-provisions these dashboards:

| Dashboard             | UID                            | Data Source | Description                            |
|-----------------------|--------------------------------|-------------|----------------------------------------|
| **API Overview**      | `pharmaciano-api`              | Prometheus  | Request rate, latency, errors, in-flight |
| **Auth & Security**   | `pharmaciano-auth-security`    | Prometheus  | Login, MFA, tokens, lockouts, RBAC, rate limits |
| **Database**          | `pharmaciano-db`               | Prometheus  | PG pool, query latency, query rate     |
| **Redis**             | `pharmaciano-redis`            | Prometheus  | Memory, hit rate, ops, eviction, session cache |
| **Infrastructure**    | `pharmaciano-infra`            | Prometheus  | CPU, memory, disk, network (node-exporter) |
| **Background Jobs & AI** | `pharmaciano-background`    | Prometheus  | Job queue, failure rate, AI cost & latency |
| **Business Metrics**  | `pharmaciano-business`         | Prometheus  | Sales, purchases, inventory alerts     |
| **Audit Trail**       | `pharmaciano-audit`            | Loki        | Real-time audit event viewer           |

### Login

- URL: http://localhost:3000
- User: `admin`
- Pass: `${GRAFANA_ADMIN_PASSWORD:-admin}`

## Audit Trail — How to View

### Method 1: Grafana Audit Dashboard (Recommended)

Navigate to **Grafana → Dashboards → Audit Trail** (or use UID `pharmaciano-audit`).

The dashboard provides:
- **Event timeline**: Volume of audit events per minute, stacked by module
- **Module breakdown**: Pie chart of events by module (auth, user, rbac, warehouse, etc.)
- **Outcome split**: Success vs failure pie chart
- **Top actions**: Bar gauge showing the most frequent actions
- **Log stream**: Full JSON audit entries with:
  - Filter by **module**, **action**, **outcome** (dropdown variables)
  - Filter by **User ID** (textbox)
  - **Free text search** across all JSON fields
  - Click any entry to expand full JSON: user_id, request_id, ip, browser, os, entity, before/after data, duration

### Method 2: Grafana Explore (Ad-hoc Queries)

Go to **Grafana → Explore → Loki** and run LogQL queries:

```logql
# All audit events
{job="pharmaciano-audit"}

# Failed logins
{job="pharmaciano-audit"} | json | module="auth" | action="login" | outcome="failure"

# Specific user's activity
{job="pharmaciano-audit"} | json | user_id="abc-123"

# RBAC changes
{job="pharmaciano-audit"} | json | module="rbac"

# All failed operations in the last hour
{job="pharmaciano-audit"} | json | outcome="failure" | unwrap created_at [1h]

# Filter by IP
{job="pharmaciano-audit"} | json | ip="192.168.1.100"

# Entity-specific (e.g., changes to product with ID 42)
{job="pharmaciano-audit"} | json | entity_type="product" | entity_id="42"
```

### Method 3: Loki HTTP API (Direct)

```bash
# Query audit logs via Loki API
curl -G http://localhost:3100/loki/api/v1/query_range \
  --data-urlencode 'query={job="pharmaciano-audit"} | json | module="auth"' \
  --data-urlencode 'start='$(date -d '1 hour ago' +%s)'000000000' \
  --data-urlencode 'end='$(date +%s)'000000000' \
  --data-urlencode 'limit=50'

# Query audit log labels
curl http://localhost:3100/loki/api/v1/labels
curl http://localhost:3100/loki/api/v1/label/module/values
```

### Method 4: Direct PostgreSQL Query (Source of Truth)

For compliance or forensic analysis requiring guaranteed completeness:

```sql
-- Recent audit trail
SELECT created_at, user_id, module, action, entity_type, entity_id,
       ip, outcome, details
FROM audit_logs
WHERE created_at > now() - interval '24 hours'
ORDER BY created_at DESC;

-- User activity report
SELECT module, action, count(*)
FROM audit_logs
WHERE user_id = 'target-user-uuid'
  AND created_at > now() - interval '7 days'
GROUP BY module, action
ORDER BY count DESC;

-- Failed operations
SELECT * FROM audit_logs
WHERE outcome = false
  AND created_at > now() - interval '1 hour';
```

## Enabling Audit-to-Loki Streaming

### Option 1: Environment Variable (Docker Compose)

Set in your `.env` or `docker-compose.override.yml`:

```yaml
environment:
  AUDIT_LOKI_ENABLED: "true"
  AUDIT_LOKI_URL: "http://loki:3100/loki/api/v1/push"
```

### Option 2: config.yaml

```yaml
audit:
  loki:
    enabled: true
    url: "http://loki:3100/loki/api/v1/push"
    batch_size: 256
    flush_interval: 500ms
    timeout: 3s
```

### Option 3: Config Overlay (Production)

In `config/config.prod.yaml`:

```yaml
audit:
  loki:
    enabled: true
    url: "http://loki:3100/loki/api/v1/push"
```

## Alert Rules

All alert rules are in `deployments/prometheus/alerts.yml` (40+ rules):

| Group              | Key Alerts                                                       |
|--------------------|------------------------------------------------------------------|
| API Availability   | APIDown, WorkerDown                                             |
| API Errors         | High5xxRate, Elevated5xxTotal                                   |
| API Latency        | HighLatencyP95, CriticalLatencyP99, HighInFlightRequests        |
| Auth & Security    | HighLoginFailureRate, AccountLockoutsSpike, RefreshTokenReuse, RateLimiting, SecurityDenials, MFAChallenges, StepUpAuth |
| Database           | DBPoolExhausted, DBPoolFull, DBQueryLatencyHigh, DBQueryErrorRate, PostgresDown |
| Redis              | RedisDown, RedisHighMemory, RedisHighEviction, RedisLatencyHigh, RedisConnectionErrors, SessionCacheHighMissRate |
| Background Jobs    | JobFailureRateHigh, JobQueueBacklog, JobLatencyHigh            |
| AI                 | AICostCapApproaching, AICostCapExceeded, AICircuitBreakerOpen   |
| Business           | LowStockAlerts, ExpiryAlerts, ZeroSalesHourly                   |
| Infrastructure     | HostHighCPU, HostHighMemory, HostDiskSpaceLow, HostDiskSpaceCritical, NodeExporterDown |
| Observability      | PrometheusTargetDown, LokiDown, PrometheusStorageFilling        |

## Configuration

### Prometheus TSDB Retention

Default: 30 days retention, 10 GB max size. Adjust in docker-compose.yml:

```yaml
command:
  - '--storage.tsdb.retention.time=90d'
  - '--storage.tsdb.retention.size=50GB'
```

### Grafana Admin Password

Set via environment variable:

```bash
GRAFANA_ADMIN_PASSWORD=your-secure-password docker compose up -d
```

### Prometheus Metrics Auth

If `telemetry.metrics.auth_token` is set in the app config, uncomment the `bearer_token` lines in `prometheus.yml` and set the same token.

## Files Reference

```
deployments/
├── docker/
│   ├── docker-compose.yml          # Full stack (core + observability)
│   └── docker-compose.override.yml # Dev overrides (ports, log levels)
├── prometheus/
│   ├── prometheus.yml              # Scrape configs (multi-target)
│   └── alerts.yml                  # 40+ production alert rules
├── grafana/
│   ├── provisioning/
│   │   ├── datasources.yml         # Prometheus + Loki datasources
│   │   └── dashboards.yml          # Auto-provision dashboards from files
│   └── dashboards/
│       ├── api.json                # API overview (6 panels)
│       ├── auth-security.json      # Auth & security (14 panels) [NEW]
│       ├── db.json                 # Database (5 panels)
│       ├── redis.json              # Redis (12 panels) [NEW]
│       ├── infrastructure.json     # Host metrics (10 panels) [NEW]
│       ├── background.json         # Jobs + AI (11 panels) [NEW]
│       ├── business.json           # Business metrics (6 panels)
│       └── audit.json              # Loki-based audit trail (5 panels) [NEW]
├── loki/
│   └── loki-config.yml             # Loki single-node config [NEW]
├── promtail/
│   └── promtail-config.yml         # Docker log scraping [NEW]
└── observability/
    └── README.md                   # This file [NEW]
```
