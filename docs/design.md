
# 📨 Messaging Microservice – Detailed Technical Design (MVP)

**Version:** 1.0  
**Status:** Finalized (detailed spec)  
**Scope:** Backend microservice — Email, SMS, and WhatsApp channels — Kafka-based message processing, validation, send, retry, status & DLQ.  
**Format:** Pseudocode + precise design; ready for engineering implementation.  
**Author:** Ajay Kumar Kukra  
**Last updated:** October 2025

---

## Table of contents

- [📨 Messaging Microservice – Detailed Technical Design (MVP)](#-messaging-microservice--detailed-technical-design-mvp)
  - [Table of contents](#table-of-contents)
  - [1. Overview](#1-overview)
    - [Tech Stack](#tech-stack)
  - [2. High-level architecture (fixed)](#2-high-level-architecture-fixed)
  - [3. Folder \& file responsibilities (canonical layout)](#3-folder--file-responsibilities-canonical-layout)
  - [4. Core data models \& JSON schemas](#4-core-data-models--json-schemas)
    - [4.1 Common envelope](#41-common-envelope)
    - [4.2 EmailRequest](#42-emailrequest)
    - [4.3 SMSRequest](#43-smsrequest)
    - [4.4 WhatsAppRequest](#44-whatsapprequest)
    - [4.5 StatusEvent](#45-statusevent)
    - [4.6 DLQRecord](#46-dlqrecord)
    - [4.7 ProviderResponse (adapter → engine)](#47-providerresponse-adapter--engine)
  - [5. Interfaces \& Pseudocode: components and wiring](#5-interfaces--pseudocode-components-and-wiring)
    - [5.1 Config Loader (pseudocode)](#51-config-loader-pseudocode)
    - [5.2 Kafka Producer (pseudocode API)](#52-kafka-producer-pseudocode-api)
    - [5.3 Kafka Consumer (pseudocode API)](#53-kafka-consumer-pseudocode-api)
    - [5.4 Worker Engine — core pseudocode (detailed)](#54-worker-engine--core-pseudocode-detailed)
    - [5.5 Adapter pseudocode (Email example)](#55-adapter-pseudocode-email-example)
    - [5.6 Provider pseudocode (HTTP client example)](#56-provider-pseudocode-http-client-example)
  - [6. Retry strategy \& backoff math](#6-retry-strategy--backoff-math)
  - [7. Error classification \& mapping](#7-error-classification--mapping)
  - [8. Validation rules \& implementation details](#8-validation-rules--implementation-details)
  - [9. Kafka behavior, offsets, and partitioning model](#9-kafka-behavior-offsets-and-partitioning-model)
  - [10. DLQ design and replay guidance](#10-dlq-design-and-replay-guidance)
  - [11. Logging, metrics \& observability hooks (detailed)](#11-logging-metrics--observability-hooks-detailed)
  - [12. Configuration \& .env.example (complete)](#12-configuration--envexample-complete)
  - [13. Testing strategy](#13-testing-strategy)
  - [14. Security, secrets \& production notes](#14-security-secrets--production-notes)
  - [15. Appendix: samples, sequence diagrams, example logs](#15-appendix-samples-sequence-diagrams-example-logs)
    - [Sample email request](#sample-email-request)
    - [Example status events timeline](#example-status-events-timeline)
    - [Example logs](#example-logs)
    - [Next steps / Handoff checklist](#next-steps--handoff-checklist)

---

## 1. Overview

This document is a developer-facing **Detailed Technical Specification** for the Messaging Microservice MVP. It includes full data models, pseudocode-level APIs and flows, exact validation and retry behaviour, Kafka offset semantics, DLQ format and replay guidance, logging and observability hooks, and configuration.

**Assumptions (MVP):**

- In-process retries (no external scheduler), at-least-once delivery semantics.  
- Providers used: SMTP for email, Twilio for SMS/WhatsApp (or equivalent HTTP APIs).  
- `.env` config in dev; secrets to be provided by secret store in production (not in scope here).

**Goal:** make the design deterministic and implementable by engineers without further architecture meetings.

### Tech Stack

| Layer | Choice |
|--------|--------|
| **Language & Runtime** | Go (Golang 1.22+) |
| **Framework** | Fiber |
| **Message Broker** | Apache Kafka |
| **Kafka Client Library** | `github.com/IBM/sarama` |
| **Database** | MongoDB *(future)* |
| **Logging** | zerolog |


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
- The worker emits **status events** (`queued`, `attempt`, `sent`, `failed`, `dlq`) to `messages.<channel>.status` and writes DLQ records to `messages.<channel>.dlq` when necessary.

**Notes**

- Kafka is the durable message bus, single source of truth for message requests.  
- Status and DLQ topics are consumed by monitoring/ops or replay tooling.

---

## 3. Folder & file responsibilities (canonical layout)

Place this skeleton in the repo as a starting point. Each file has a short responsibility comment.

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
│   ├── produce-sample-email.sh   # helper to produce sample messages to Kafka
│   └── run-local.sh              # local startup helper that runs kafka + workers
├── test/
│   └── docker-compose.yml        # kafka, zookeeper, schema-registry (optional), and mock providers
├── .env                          # local env (do not commit)
├── .env.example                  # sample env committed
├── .gitignore
├── Makefile
└── README.md
```

**Implementation notes**

- Keep file responsibilities narrow. Adapters handle mapping & classification; providers only do network I/O; engine handles orchestration and retries.  
- Unit tests sit alongside each package (e.g., `internal/util/validation_test.go`).

---

## 4. Core data models & JSON schemas

This section lists complete request/status/DLQ schemas and the `ProviderResponse` model used between adapter and engine.

### 4.1 Common envelope
```json
{
  "message_id": "uuid-v4",
  "channel": "email|sms|whatsapp",
  "tenant_id": "optional",
  "trace_id": "optional",
  "created_at": "RFC3339 UTC",
  "meta": { "k":"v" }
}
```

### 4.2 EmailRequest
```json
{
  "message_id": "uuid",
  "channel": "email",
  "created_at": "2025-10-11T10:00:00Z",
  "from": "noreply@example.com",
  "to": ["user1@example.com","user2@example.com"],
  "cc": ["cc@example.com"],
  "bcc": null,
  "subject": "Hello",
  "body": { "type":"html","content":"<p>Hi</p>" },
  "meta": { }
}
```

### 4.3 SMSRequest
```json
{
  "message_id":"uuid",
  "channel":"sms",
  "created_at":"RFC3339",
  "from":"+1234567890",
  "to":["+19876543210"],
  "body":{ "type":"text", "content":"Short SMS text" }
}
```

### 4.4 WhatsAppRequest
```json
{
  "message_id":"uuid",
  "channel":"whatsapp",
  "created_at":"RFC3339",
  "from":"+1234567890",
  "to":["+19876543210"],
  "body":{ "type":"text|template|media", "content":"...", "media_type":"image/png" }
}
```

### 4.5 StatusEvent
```json
{
  "message_id":"uuid",
  "channel":"email|sms|whatsapp",
  "event_type":"queued|attempt|sent|rejected|rate_limited|failed|dlq",
  "attempt":1,
  "provider_response":{...},
  "error":null,
  "trace_id":null,
  "timestamp":"RFC3339"
}
```

### 4.6 DLQRecord
```json
{
  "message_id":"uuid",
  "channel":"email",
  "original_message":{ ... },
  "attempts":3,
  "failure_type":"permanent|transient|validation|unknown",
  "last_error":"string",
  "first_failed_at":"RFC3339",
  "last_attempt_at":"RFC3339",
  "trace_id":null
}
```

### 4.7 ProviderResponse (adapter → engine)
```
ProviderResponse {
  status: string (ok|queued|rejected|failed|rate_limited|unknown)
  code: int|null
  message: string|null
  raw: string|null  // trimmed
  meta: map[string]string
}
```

Adapters return `(ProviderResponse, error)` where `error` is wrapped as `ErrTransient` or `ErrPermanent` as appropriate.

---

## 5. Interfaces & Pseudocode: components and wiring

This section contains the exact pseudocode that engineers should implement.

### 5.1 Config Loader (pseudocode)
```text
func LoadConfig() -> (*Config, error) {
  godotenv.Load() // dev optional
  parse env vars into Config struct
  apply defaults for missing non-critical values
  validate required fields (kafka brokers, topics, provider creds for channel)
  return cfg
}
```

### 5.2 Kafka Producer (pseudocode API)
```text
Producer := NewProducer(brokers, logger)
Producer.PublishSync(topic, key, headers, payload) -> error
Producer.PublishAsync(topic, key, headers, payload) -> error
Producer.IsReady() -> bool
Producer.Close()
```

Producer should maintain a metadata cache refreshed periodically for readiness checks.

### 5.3 Kafka Consumer (pseudocode API)
```text
Consumer := NewConsumer(brokers, groupID, logger, commitOnSuccessOnly)
Consumer.Consume(ctx, topic, handlerFunc) // handlerFunc(ctx, record) error
Consumer.Commit(record)
Consumer.IsReady()
Consumer.Close()
```

Consumer should operate in manual commit mode when `COMMIT_ON_SUCCESS_ONLY=true`.

### 5.4 Worker Engine — core pseudocode (detailed)
```text
func HandleRecord(ctx, record) {
  // 1. size check
  if len(record.Value) > cfg.MsgMaxBytes {
    publishValidationDLQ(...)
    consumer.Commit(record)
    return
  }

  // 2. parse & validate
  req, err := ParseAndValidate(record.Value, cfg.Channel)
  if err != nil {
    publishStatus(req, "failed", attempt=0, error=err.Error())
    publishDLQ(req, failure_type="validation", last_error=err.Error())
    consumer.Commit(record)
    return
  }

  // 3. concurrency guard
  sem.Acquire()
  go func() {
    defer sem.Release()
    processMessage(ctx, req, record)
  }()
}

func processMessage(ctx, req, record) {
  attempt := 1
  max := cfg.MaxAttempts
  base := cfg.BaseBackoffSeconds
  maxBackoff := cfg.MaxBackoffSeconds

  publishStatus(req, "queued", attempt=0)

  firstFailedAt := nil
  for {
    publishStatus(req, "attempt", attempt)
    start := now()
    providerResp, err := adapter.Send(ctx, req)
    duration := now() - start
    log attempt with duration

    if err == nil {
      publishStatus(req, "sent", attempt, providerResp)
      consumer.Commit(record)
      return
    }

    if errors.Is(err, adapters.ErrPermanent) {
      publishStatus(req, "failed", attempt, providerResp, err)
      publishDLQ(req, attempts=attempt, failure_type="permanent", last_error=err.Error(), first_failed_at=firstFailedAtOrNow)
      consumer.Commit(record)
      return
    }

    // transient
    if firstFailedAt == nil { firstFailedAt = now() }
    if attempt >= max {
      publishStatus(req, "failed", attempt, providerResp, err)
      publishDLQ(req, attempts=attempt, failure_type="transient", last_error=err.Error(), first_failed_at=firstFailedAt)
      consumer.Commit(record)
      return
    }

    delay := computeBackoffWithJitter(base, attempt, maxBackoff)
    sleep(delay)
    attempt++
    continue
  }
}
```

**Notes:**
- `consumer.Commit(record)` must be idempotent-safe and use the consumer wrapper.  
- `publishStatus` and `publishDLQ` use `Producer.PublishSync` to ensure status/dlq writes are durable before committing offsets (recommended for accuracy).

### 5.5 Adapter pseudocode (Email example)
```text
func EmailAdapter.Send(ctx, req EmailRequest) -> (ProviderResponse, error) {
  // normalize
  payload := buildSMTPPayload(req)
  // add Message-ID header equal to req.MessageID (for provider-side dedupe if supported)

  rawResp, err := smtpProvider.Send(ctx, payload)
  if err == nil {
    return ProviderResponse{status:"ok", code:200, message:"sent", meta:{"provider_id":rawResp.Id}, raw:trim(rawResp.Body, 1024)}, nil
  }

  // classification
  if isSMTP550Recipient(err) {
    return ProviderResponse{status:"rejected", code:550, message:"recipient rejected"}, wrap(ErrPermanent, err)
  }
  if is429(err) or isTimeout(err) or is5xx(err) {
    return ProviderResponse{status:"rate_limited", code:getCode(err), message:err.Error()}, wrap(ErrTransient, err)
  }
  // fallback
  return ProviderResponse{status:"unknown", message:err.Error()}, wrap(ErrTransient, err)
}
```

SMS and WhatsApp adapters follow similar pattern but use Twilio-specific error codes (use Twilio docs mapping).

### 5.6 Provider pseudocode (HTTP client example)
```text
func (p *TwilioClient) SendSMS(ctx, payload) -> (RawResp, error) {
  req := http.NewRequest("POST", p.baseURL+"/Messages.json", payload)
  req.SetBasicAuth(p.sid, p.token)
  client := http.Client{Timeout: p.timeout}
  resp, err := client.Do(req)
  if err != nil { return RawResp{}, err }
  body := readAll(resp.Body)
  if resp.StatusCode >= 200 && resp.StatusCode < 300 {
    return RawResp{Code:resp.StatusCode, Body: string(body), Id: parseSid(body)}, nil
  }
  return RawResp{Code:resp.StatusCode, Body: string(body)}, fmt.Errorf("http %d: %s", resp.StatusCode, body)
}
```

Providers should avoid classification; they return raw HTTP status and body for adapters to interpret.

---

## 6. Retry strategy & backoff math

**Defaults**
- `MAX_ATTEMPTS = 3`  
- `BASE_BACKOFF_SECONDS = 10`  
- `MAX_BACKOFF_SECONDS = 120`  
- `BACKOFF_JITTER = full`  

**Exact formula**
- base := `BASE_BACKOFF_SECONDS`  
- for attempt N (>1): `rawDelay = base * 2^(N-2)`  
- `delay = min(rawDelay, MAX_BACKOFF_SECONDS)`  
- if `BACKOFF_JITTER == full`: `wait = rand(0, delay)` else `wait = delay`

**Example**
- attempt1: immediate  
- attempt2: random(0..10s)  
- attempt3: random(0..20s)

**Edge cases**
- `ErrPermanent` leads to immediate DLQ, no wait.  
- If engine crashes during wait, message will be redelivered by Kafka; duplicates acceptable for MVP.

---

## 7. Error classification & mapping

Adapters must return errors wrapped with sentinel errors: `ErrTransient` and `ErrPermanent` (in `adapters/common/errors.go`).

**High-level mapping guidelines**
- Network timeouts, 5xx, 429 → `ErrTransient`  
- Invalid recipient, unsubscribed, bad request → `ErrPermanent`  
- Authentication invalid → `ErrPermanent` (alert ops)  
- Unknown/unclassifiable → `ErrTransient` (safer default)

**Examples**
- SMTP 250 → success  
- SMTP 550 → `ErrPermanent` (rejected)  
- Twilio 21614 (invalid number) → `ErrPermanent`  
- Twilio 429 → `ErrTransient`

Engine checks `errors.Is(err, ErrPermanent)` to decide DLQ immediacy.

---

## 8. Validation rules & implementation details

Validation must be deterministic and fast. Do validations before contacting providers.

**Common**
- `message_id`: UUID v4 (strict parser)  
- `created_at`: RFC3339 → `time.Time`  
- `channel`: equals worker channel  
- `meta`: entries ≤ `META_MAX_ENTRIES`, key len ≤ `META_MAX_KEY_LEN`, value len ≤ `META_MAX_VALUE_LEN`  
- message JSON size ≤ `MSG_MAX_BYTES`

**Email specifics**
- `from`: valid email (use library)  
- `to`: 1..`RECIPIENTS_MAX`, each valid email  
- `subject`: non-empty, length ≤ `SUBJECT_MAX_LEN`  
- `body.content` length ≤ `BODY_MAX_BYTES`

**SMS specifics**
- `to`: E.164 format (regex: `^\+[1-9]\d{1,14}$`)  
- recipients ≤ `SMS_RECIPIENTS_MAX`  
- `body` length ≤ `SMS_BODY_MAX`

**WhatsApp specifics**
- `to`: E.164  
- `template`: require template id pattern if `type==template`  
- `media`: `content` must be valid URL (http/https)

**On validation failure**
- publish status `failed` with error  
- publish DLQ with `failure_type=validation`  
- commit offset

---

## 9. Kafka behavior, offsets, and partitioning model

- Topic naming: `messages.<channel>.<purpose>` (purpose in `{request,status,dlq}`)  
- Key: `message_id` (ensures lifecycle records land in same partition)  
- Partitions: start with 6 for request topics; adjust with throughput  
- Replication factor: 3 in prod (1 in dev)

**Offset semantics**
- Manual commit only after final handling (success or DLQ)  
- If commit fails, retry commit but avoid double-processing logic; commit must be retried before exiting

**Consumer group naming**
- `<channel>-worker-group` (e.g., `email-worker-group`)

**Ordering**
- Per-message ordering guaranteed within partition because key is `message_id`  
- For tenant-level ordering later, use key `tenantID|messageID` (tradeoff: distribution vs ordering)

---

## 10. DLQ design and replay guidance

**DLQ record fields**
- `original_message` (full)  
- `attempts`  
- `failure_type` (`permanent|transient|validation|unknown`)  
- `last_error`  
- `first_failed_at`  
- `last_attempt_at`  
- `trace_id`

**Replay steps (manual)**
1. Inspect DLQ entry  
2. Fix issue (if validation or data issue)  
3. Re-publish `original_message` to `messages.<channel>.request`  
4. Add `meta.replayed_from` and increment `meta.replay_count` to avoid infinite loops

**Automation (future)**
- Replay tool that reads DLQ, applies filters, optionally fixes data, re-publishes with audit logs.

---

## 11. Logging, metrics & observability hooks (detailed)

**Structured logs (zerolog)** — required fields:
- `time`, `level`, `message`, `channel`, `message_id`, `event`, `attempt`, `provider_status`, `provider_code`, `err`, `trace_id`, `duration_ms`

**Metrics (names only)**
- `messages_attempt_total{channel}`  
- `messages_sent_total{channel}`  
- `messages_failed_total{channel}`  
- `messages_dlq_total{channel}`  
- `message_attempt_duration_seconds`  
- `worker_concurrency_active`

Implement hooks where counters increment; if no metrics backend in MVP, log periodic summaries.

**Tracing**
- Propagate `trace_id` in Kafka headers and provider API calls.

---

## 12. Configuration & .env.example (complete)

Place `.env.example` in repo root — copy into `.env` locally (do not commit `.env`). Example values:

```text
# App
APP_ENV=development
APP_PORT=8080
LOG_LEVEL=info

# Kafka
KAFKA_BROKERS=localhost:9092
KAFKA_REQUEST_PARTITIONS=6
KAFKA_REPLICATION_FACTOR=1

# Topics
KAFKA_EMAIL_REQUEST_TOPIC=messages.email.request
KAFKA_EMAIL_STATUS_TOPIC=messages.email.status
KAFKA_EMAIL_DLQ_TOPIC=messages.email.dlq

KAFKA_SMS_REQUEST_TOPIC=messages.sms.request
KAFKA_SMS_STATUS_TOPIC=messages.sms.status
KAFKA_SMS_DLQ_TOPIC=messages.sms.dlq

KAFKA_WHATSAPP_REQUEST_TOPIC=messages.whatsapp.request
KAFKA_WHATSAPP_STATUS_TOPIC=messages.whatsapp.status
KAFKA_WHATSAPP_DLQ_TOPIC=messages.whatsapp.dlq

# Consumer groups
EMAIL_CONSUMER_GROUP=email-worker-group
SMS_CONSUMER_GROUP=sms-worker-group
WHATSAPP_CONSUMER_GROUP=whatsapp-worker-group

# Retry / Worker
MAX_ATTEMPTS=3
BASE_BACKOFF_SECONDS=10
MAX_BACKOFF_SECONDS=120
BACKOFF_STRATEGY=exponential
BACKOFF_JITTER=full
WORKER_CONCURRENCY=10
COMMIT_ON_SUCCESS_ONLY=true

# Validation limits
MSG_MAX_BYTES=200000
RECIPIENTS_MAX=50
SUBJECT_MAX_LEN=255
BODY_MAX_BYTES=100000
SMS_RECIPIENTS_MAX=10
SMS_BODY_MAX=1600
WA_BODY_MAX=4096
META_MAX_ENTRIES=20
META_MAX_KEY_LEN=64
META_MAX_VALUE_LEN=256

# Providers (local dev only)
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USER=noreply@example.com
SMTP_PASS=changeme
SMTP_FROM=noreply@example.com

TWILIO_ACCOUNT_SID=your_sid
TWILIO_AUTH_TOKEN=your_token
TWILIO_PHONE_NUMBER=+1234567890

# Timeouts
PROVIDER_TIMEOUT_SECONDS=30

# Health
HEALTH_POLL_INTERVAL_SECONDS=15
HEALTH_CHECK_TIMEOUT_MS=200
HEALTH_HANDLER_TIMEOUT_MS=500
HEALTH_ENABLE_PROVIDER_PROBE=false
```

Parsing notes: split `KAFKA_BROKERS` by comma, parse ints, booleans. Fail fast if required channel provider credentials missing.

---

## 13. Testing strategy

**Unit tests**
- validators, adapters (with mocked providers), engine (mock adapters + producer)

**Integration tests (local)**
- docker-compose with Kafka and mock providers
- test scripts produce messages and assert status/dlq topics

**Load / Chaos tests**
- run load generator; measure consumer lag; simulate provider outages and verify retry/backoff and DLQ

---

## 14. Security, secrets & production notes

- Do NOT commit `.env` with secrets. Use secrets manager in prod (K8s secrets, Vault, AWS Secrets Manager).  
- Use TLS and SASL for Kafka in prod; restrict network egress to provider IPs.  
- Health endpoints should be internal-only or protected.  
- Redact provider responses and avoid logging sensitive data.

---

## 15. Appendix: samples, sequence diagrams, example logs

### Sample email request
```json
{
  "message_id":"b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
  "channel":"email",
  "trace_id":"trace-abc-0001",
  "created_at":"2025-10-11T10:00:00Z",
  "from":"noreply@example.com",
  "to":["user@example.com"],
  "subject":"Welcome!",
  "body":{"type":"html","content":"<p>Welcome!</p>"}
}
```

### Example status events timeline

1. `queued` (worker claimed message)  
2. `attempt` (attempt=1)  
3. transient error → retry after backoff  
4. `attempt` (attempt=2)  
5. `sent` → commit offset

### Example logs

Attempt warn:
```json
{ "time":"2025-10-11T10:00:02Z", "level":"warn", "message":"email send attempt failed, will retry", "channel":"email", "message_id":"...", "event":"attempt", "attempt":1, "err":"timeout", "trace_id":"trace-abc-0001", "duration_ms":500 }
```

DLQ error:
```json
{ "time":"2025-10-11T10:00:45Z", "level":"error", "message":"message moved to DLQ", "channel":"email", "message_id":"...", "event":"dlq", "attempts":3, "failure_type":"transient", "last_error":"network timeout", "trace_id":"trace-abc-0001" }
```

---

### Next steps / Handoff checklist

1. Commit repo skeleton and `.env.example`.  
2. Implement `config.Load()` and `logger.New()`.  
3. Implement Kafka producer/consumer wrappers with IsReady caches.  
4. Implement `worker.Engine` using the pseudocode above.  
5. Implement adapters and mock providers, run integration tests.

---

