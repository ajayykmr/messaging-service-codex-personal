# 📨 Messaging Workers – Implementation Design

**Version:** 1.1  
**Status:** Implemented  
**Scope:** Kafka-driven background workers for Email, SMS, WhatsApp.  
**Author:** Ajay Kumar Kukra  
**Last updated:** 16 Oct 2025

---

## Table of contents

- [1. Overview](#1-overview)
  - [Tech Stack](#tech-stack)
- [2. Architecture](#2-architecture)
- [3. Package layout](#3-package-layout)
- [4. Configuration](#4-configuration)
- [5. Data models](#5-data-models)
- [6. Validation rules](#6-validation-rules)
- [7. Worker engine](#7-worker-engine)
- [8. Kafka components](#8-kafka-components)
- [9. Adapters and providers](#9-adapters-and-providers)
- [10. Status and DLQ events](#10-status-and-dlq-events)
- [11. Logging and observability](#11-logging-and-observability)
- [12. Tooling](#12-tooling)
- [13. Testing](#13-testing)
- [14. Known gaps](#14-known-gaps)

---

## 1. Overview

The repository implements the messaging worker service described in the MVP spec. Three binaries (`email-worker`, `sms-worker`, `whatsapp-worker`) run the same processing pipeline with channel-specific validation, adapters, and providers. Each worker:

- Loads configuration from environment variables (via `config.Load`).
- Creates a shared Kafka producer and per-channel consumer group.
- Builds a provider using `internal/providers/factory`.
- Assembles the worker engine with the appropriate validator and adapter.
- Consumes the channel request topic, publishes status/DLQ events, and commits offsets after durable writes.

There is no HTTP ingress component yet; health endpoints are stubbed for future use. The project targets at-least-once delivery with explicit retry and DLQ handling.

### Tech Stack

| Layer | Implementation |
|-------|----------------|
| Language & runtime | Go 1.22 |
| Dependency mgmt | Go modules |
| Messaging | Apache Kafka via `github.com/IBM/sarama` |
| Logging | `github.com/rs/zerolog` (console in dev, JSON otherwise) |
| Config | Environment variables + `github.com/joho/godotenv` |
| Concurrency utilities | `golang.org/x/sync/semaphore` |
| External providers | SMTP (email), Twilio (SMS/WhatsApp) with mock fallbacks |

---

## 2. High-level architecture (fixed)

The diagram below shows directional flow clearly for requests and status events. There are separate arrows for request flow (producer → kafka → worker → adapter → provider) and status/DLQ flow (worker → kafka).

```
(Upstream) Producers
  (API, backend, scheduled jobs)
       |
       |  --produce-->  (write request messages)
       v
  +-----------------+
  |   Kafka Topics  |
  |                 |
  | messages.<ch>.request   <-- producers publish requests
  | messages.<ch>.status    <-- workers publish lifecycle events
  | messages.<ch>.dlq       <-- workers publish failures
  +-----------------+
       |
       |  --consume-->  (worker instances read requests)
       v
  +-----------------+
  | Channel Worker  |  --call-->  Adapter  --call-->  Provider (SMTP/Twilio)
  | (email/sms/wa)  |  <--resp--  Adapter  <--resp--  Provider
  +-----------------+
       |
       |  --publish status/dlq-->  (workers write status and dlq back to Kafka)
       v
  +-----------------+
  |   Kafka Topics  |
  +-----------------+
```

**Narrative**

- **Producers / upstream systems** publish send requests to `messages.<channel>.request` topics.  
- **Channel Workers** (multiple instances) consume from the request topic, validate and orchestrate sending.  
- A worker calls the **Adapter**, which normalizes request and calls the **Provider** (SMTP/Twilio). Adapter classifies the response and returns a normalized `ProviderResponse` and a classified error (transient/permanent).  
 - The worker emits **status events** (`queued`, `attempt`, `sent`, `failed`) to `messages.<channel>.status` and writes DLQ records to `messages.<channel>.dlq` when necessary.

**Notes**

- Kafka is the durable message bus, single source of truth for message requests.  
- Status and DLQ topics are consumed by monitoring/ops or replay tooling.

---

## 3. Package layout

```
messaging-microservice/
├── cmd/
│   ├── email-worker/
│   │   └── main.go               # bootstrap: config.Load(), logger.New(), init kafka producer+consumer, provider, adapter, engine, health server
│   ├── sms-worker/
│   │   └── main.go
│   └── whatsapp-worker/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go             # Load .env, parse/validate env vars, expose Config struct
│   ├── logger/
│   │   └── logger.go             # Initialize zerolog logger (console in dev, JSON in prod)
│   ├── kafka/
│   │   ├── consumer/
│   │   │   └── consumer.go       # Consumer wrapper: manual commits, IsReady cache, health probe hooks
│   │   └── producer/
│   │       └── producer.go       # Producer wrapper: sync/async publish, IsReady cache
│   ├── models/
│   │   ├── request.go            # EmailRequest, SMSRequest, WhatsAppRequest structs + JSON tags
│   │   ├── status.go             # StatusEvent struct + JSON tags
│   │   └── dlq.go                # DLQRecord struct
│   ├── adapters/
│   │   ├── common/
│   │   │   ├── errors.go         # ErrTransient, ErrPermanent sentinel errors
│   │   │   └── provider_response.go # ProviderResponse struct
│   │   ├── email/
│   │   │   └── adapter.go        # EmailAdapter: maps EmailRequest -> ProviderResponse, classify errors
│   │   ├── sms/
│   │   │   └── adapter.go        # SMSAdapter
│   │   └── whatsapp/
│   │       └── adapter.go        # WhatsAppAdapter
│   ├── providers/
│   │   ├── email/
│   │   │   └── provider.go       # SMTPProvider: low-level SMTP/send wrapper
│   │   ├── sms/
│   │   │   └── provider.go       # TwilioClient: SMS send
│   │   └── whatsapp/
│   │       └── provider.go       # WhatsApp send (via Twilio or Meta API)
│   ├── worker/
│   │   └── engine.go             # Engine: Handle(record) -> validation, send attempts, backoff, DLQ, commit
│   ├── health/
│   │   └── health.go             # HTTP handlers for /healthz/live and /healthz/ready and readiness cache
│   └── util/
│       └── validation.go         # Validators: UUID, RFC3339, email, E.164, size checks
├── scripts/
│   ├── email-scenario-producer.sh   # emit sample channel payloads with failure scenarios
│   ├── kafka-producer-load-test.sh  # generate high-volume load for stress tests
│   ├── kafka-init.sh                # create topics/quotas in the local cluster
│   └── check-kafka-connection.sh    # sanity-check broker connectivity from tooling
├── test/
│   └── docker-compose.yml        # kafka, zookeeper, schema-registry (optional), and mock providers
├── .env                          # local env (do not commit)
├── .env.example                  # sample env committed
├── .gitignore
├── Makefile
└── README.md
```

`internal/health` currently contains only a package stub. Everything else above is exercised by the workers.

---

## 4. Configuration

`config.Load` populates the following structure:

- `App`: `APP_ENV` (default `development`), `APP_PORT` (8080, reserved for future health server), `LOG_LEVEL` (info).
- `Kafka`: `KAFKA_BROKERS` (required, comma-separated), `KAFKA_REQUEST_PARTITIONS` (default 6), `KAFKA_REPLICATION_FACTOR` (default 1).
- `Topics`: request/status/DLQ topics for each channel (all required).
- `ConsumerGroups`: three required group names.
- `Retry`: `MAX_ATTEMPTS` (default 3), `BASE_BACKOFF_SECONDS` (10), `MAX_BACKOFF_SECONDS` (120), `BACKOFF_STRATEGY` and `BACKOFF_JITTER` (captured but currently informational), `WORKER_CONCURRENCY` (10), `COMMIT_ON_SUCCESS_ONLY` (true).
- `Validation`: limits for payload size, recipients, metadata, and per-channel body sizes (defaults match those in `config.Load`).
- `Providers`: `EMAIL_PROVIDER`, `SMS_PROVIDER`, `WHATSAPP_PROVIDER` (defaults `mock`); SMTP and Twilio credentials are required only when their respective providers are selected.
- `Timeouts`: `PROVIDER_TIMEOUT_SECONDS` (30) – reserved for future provider wrappers.
- `Health`: polling and timeout values plus `HEALTH_ENABLE_PROVIDER_PROBE` (all default to conservative values; not yet used).

`.env.example` mirrors these keys and provides production-leaning overrides (higher limits, replication factor 3, backoff tuned to 5 attempts). `KAFKA_TEMP_TOPIC` appears in the example for script tooling but is not currently read by the service.

The loader trims whitespace, normalises provider names, enforces allowed provider values (`smtp|mock` for email, `twilio|mock` for SMS/WhatsApp), and aggregates validation errors before returning.

---

## 5. Data models

`internal/models` holds the canonical structs shared across validators, adapters, and publishers.

```go
type Envelope struct {
    MessageID string            `json:"message_id"`
    Channel   string            `json:"channel"`
    TenantID  string            `json:"tenant_id,omitempty"`
    TraceID   string            `json:"trace_id,omitempty"`
    CreatedAt time.Time         `json:"created_at"`
    Meta      map[string]string `json:"meta,omitempty"`
}

type EmailRequest struct {
    Envelope
    From    string      `json:"from"`
    To      []string    `json:"to"`
    CC      []string    `json:"cc,omitempty"`
    BCC     []string    `json:"bcc,omitempty"`
    Subject string      `json:"subject"`
    Body    MessageBody `json:"body"`
}

type SMSRequest struct {
    Envelope
    From string      `json:"from"`
    To   []string    `json:"to"`
    Body MessageBody `json:"body"`
}

type WhatsAppRequest struct {
    Envelope
    From string      `json:"from"`
    To   []string    `json:"to"`
    Body MessageBody `json:"body"`
}
```

Status and DLQ payloads follow `models.StatusEvent` and `models.DLQRecord`, matching the JSON emitted by the publishers. `worker.ValidatedMessage` carries the parsed request, metadata, raw payload, Kafka key, and headers between stages.

---

## 6. Validation rules

Channel validators sit under `internal/worker/validator/<channel>` and share helper utilities from `internal/util`.

Common rules:

- `message_id`: UUID v4 via `util.ParseUUIDv4`.
- `created_at`: must be present and is normalised to UTC.
- `channel`: defaults to the worker’s configured channel; mismatches are rejected.
- `meta`: validated with `util.ValidateMetadata` (entry count, key/value length).
- Payload size: `worker.Engine` enforces `MSG_MAX_BYTES` before validation.

Email specifics:

- Sender and recipient lists normalised with `util.NormalizeEmail`.
- Subject length enforced via `config.Validation.SubjectMaxLen`.
- Body type defaults to `text`; only `text` and `html` are accepted.
- Body length checked against `BODY_MAX_BYTES`.

SMS specifics:

- From/To numbers validated with `util.NormalizeE164` and `NormalizeE164List`.
- Body type fixed to `text`; rune length limited by `SMS_BODY_MAX`.

WhatsApp specifics:

- Phone numbers subject to the same E.164 validation.
- Body type must be `text`, `template`, or `media`; defaults to `text`.
- Body length capped by `WA_BODY_MAX`.

Validation errors trigger a status event (`failed`, attempt 0) and an immediate DLQ record with `failure_type=validation`.

---

## 7. Worker engine

`internal/worker/engine.go` coordinates processing:

1. Size guard: discards payloads above `MsgMaxBytes`, emits validation DLQ, commits offset.
2. Parse & validate: channel validator returns a `ValidatedMessage`; failures follow the validation path above.
3. Concurrency control: a weighted semaphore (`WorkerConcurrency`) bounds in-flight processing.
4. Processing loop:
   - Publish `queued`, then for each attempt publish `attempt`.
   - Call the adapter. On success publish `sent`, commit offset.
   - On `common.ErrPermanent` publish `failed`, emit DLQ (`failure_type=permanent`), commit.
   - On transient/unknown errors:
     - Publish `failed` only after retries are exhausted.
     - Backoff uses exponential growth with full jitter: `backoff = rand[0, min(BaseBackoff*2^(attempt-1), MaxBackoff)]`.
     - The loop stops early if the context is cancelled; the record will be reprocessed.
5. Status and DLQ publishes happen before commits. Any publish error is logged with channel/message_id context.

`Record.Clone` ensures safe use from goroutines, and the engine remembers timestamps required to populate DLQ metadata (`first_failed_at`, `last_attempt_at`).

---

## 8. Kafka components

- `internal/kafka/producer` wraps Sarama sync/async producers, tracks readiness via periodic metadata refresh, and logs async errors.
- `internal/kafka/consumer` wraps a consumer group with manual commit support. `COMMIT_ON_SUCCESS_ONLY=true` switches to synchronous commits after every processed record.
- `internal/kafka/publisher` exposes `StatusPublisher` and `DLQPublisher`, each using the shared producer to emit JSON payloads synchronously.

Consumer handlers are adapted via `worker.KafkaHandler`, which binds the consumer commit function to the worker record.

---

## 9. Adapters and providers

Adapters convert validated messages to provider payloads, invoke providers, classify responses, and wrap errors with `common.WrapTransient`/`WrapPermanent`.

- Email (`internal/adapters/email`): builds SMTP payloads (including Message-ID header), truncates provider bodies to 1024 runes, classifies SMTP error codes (e.g., 550 → `rejected`, 450/4xx → `rate_limited`), and surfaces metadata such as provider ID and timestamp.
- SMS (`internal/adapters/sms`): maps Twilio (or mock) responses, inspects Twilio error codes (21610, 21614, etc.), HTTP status, and textual hints to decide between `rejected`, `rate_limited`, or `unknown`.
- WhatsApp (`internal/adapters/whatsapp`): mirrors the SMS adapter, supporting text/template/media body types and the same classification approach.

Providers:

- SMTP: full STARTTLS SMTP client with configurable dialer, auth, TLS, and EHLO name.
- Twilio (SMS/WhatsApp): REST API integration with request signing, response parsing, selective metadata capture, and per-recipient loop to aggregate IDs/status.
- Mock providers: deterministic behaviour for local testing, supporting scenario overrides via metadata (e.g., transient/permanent errors).

`internal/providers/factory` selects the concrete provider based on config, logging the chosen backend.

---

## 10. Status and DLQ events

Status events (`models.StatusEvent`) currently emit the following `event_type` values: `queued`, `attempt`, `sent`, `failed`. Additional constants (`rejected`, `rate_limited`, `dlq`) exist for future enrichment. The payload also includes:

- `attempt`: populated for attempt/sent/failed events.
- `provider_response`: mirrors `common.ProviderResponse` (status/code/message/raw/meta). Adapter status values include `ok`, `rejected`, `rate_limited`, `unknown`.
- `error`: populated when applicable.
- `timestamp`: set server-side during publication.

DLQ records (`models.DLQRecord`) capture the original payload (JSON or base64), attempt count, failure type (`permanent|transient|validation|unknown`), last error, timestamps, trace ID, and metadata derived from the validated message.

---

## 11. Logging and observability

- `logger.New` configures zerolog with human-friendly console output in development and JSON elsewhere. Global level follows `LOG_LEVEL`.
- Producers and consumers log readiness and error conditions with structured fields (`channel`, `message_id`, `attempt`, `provider_status`, etc.).
- `Producer.IsReady()` and `Consumer.IsReady()` expose health hints for future probes.
- `internal/health` currently contains only a placeholder; no HTTP handlers are exposed yet.
- Metrics and tracing are not implemented; logs are the primary observability surface.

---

## 12. Tooling

Scripts under `scripts/` support local operations:

- `kafka-init.sh`: bootstrap Kafka topics according to `.env`.
- `email-scenario-producer.sh`: emit sample email requests with optional failure scenarios.
- `kafka-producer-load-test.sh`: generate high-volume load with tunable payloads and metadata.
- `check-kafka-connection.sh`: quick readiness probe using `KAFKA_TEMP_TOPIC`.

All scripts source `.env` (or a provided env file) and honour overrides supplied via CLI flags.

---

## 13. Testing

The `test/` tree contains Go tests for critical packages:

- `test/config`: exercises env parsing, defaults, and validation.
- `test/logger`: validates log level parsing and console writer behaviour.
- `test/util`: covers UUID/email/phone/metadata helpers.
- `test/worker`: unit-tests the engine using fake adapters/publishers to confirm retry, DLQ, and status flows.

Add new tests alongside the relevant packages or under `test/` following the existing pattern.

---

## 14. Known gaps

- Health/endpoints: `internal/health` is not wired into the workers; `APP_PORT` remains unused.
- Metrics/tracing: no Prometheus or OpenTelemetry instrumentation yet.
- Timeout config: `PROVIDER_TIMEOUT_SECONDS` is captured but not applied; providers use hard-coded defaults.
- Alternative backoff strategies and jitter settings are stored in config but the engine currently implements exponential backoff with full jitter only.
- `.env.example` includes `KAFKA_TEMP_TOPIC` purely for script support; the service ignores it.

These items can be addressed iteratively without refactoring the existing worker pipeline.
