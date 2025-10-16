# Messaging Worker Service

Kafka-backed background workers for processing outbound Email, SMS, and WhatsApp messages. The service validates inbound payloads, invokes channel providers (SMTP or Twilio, with mock fallbacks), and publishes lifecycle events plus DLQ entries for downstream observability. Implementation details are captured in `docs/design.md`.

---

## Highlights

- Three independent worker binaries (`cmd/email-worker`, `cmd/sms-worker`, `cmd/whatsapp-worker`).
- Config-driven setup with environment defaults and `.env.example` for production-ready values.
- Pluggable provider layer (SMTP/Twilio/mock) selected at runtime.
- Structured status events and DLQ records emitted before committing Kafka offsets.
- Exponential backoff with full jitter and retry budget per channel.
- Comprehensive validation for message envelopes, recipients, metadata, and payload sizes.

See the original specification in `docs/design_initial.md` and the implementation design in `docs/design.md`.

---

## Architecture in brief

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

For a full component walkthrough, read `docs/design.md`.

---

## Message payloads

The tables below describe the exact payloads that upstream producers must publish and the events downstream consumers should expect. Unless noted, timestamps are RFC3339 (preferably RFC3339Nano) in UTC, string values are trimmed, and metadata maps must contain string keys/values.

### Email request (`messages.email.request`)

- **Kafka key:** `message_id` (UUID v4, lower-case canonical form)
- **Fields:**

  | Field | Required | Type | Constraints |
  |-------|----------|------|-------------|
  | `message_id` | Yes | string | UUID v4 |
  | `channel` | Yes | string | Must be `email` |
  | `tenant_id` | No | string | Optional tenant identifier |
  | `trace_id` | No | string | Optional distributed-trace identifier |
  | `created_at` | Yes | string | RFC3339 timestamp (UTC) |
  | `meta` | No | object | ≤50 entries; keys ≤128 runes; values ≤1024 runes |
  | `from` | Yes | string | RFC 5321 email address without display name |
  | `to` | Yes | array[string] | 1–100 recipients, each valid email |
  | `cc` | No | array[string] | 0–100 valid email addresses |
  | `bcc` | No | array[string] | 0–100 valid email addresses |
  | `subject` | No (recommended) | string | ≤255 runes |
  | `body.type` | No | string | Defaults to `text`; allowed values: `text`, `html` |
  | `body.content` | Yes | string | ≤2,097,152 bytes |

- **Example:**

  ```json
  {
    "message_id": "1e8f3b1b-4d0d-4bd3-9c86-2f72c7808c61",
    "channel": "email",
    "tenant_id": "acme",
    "trace_id": "trace-123",
    "created_at": "2025-10-16T09:30:00Z",
    "meta": {
      "campaign": "autumn-cta"
    },
    "from": "noreply@example.com",
    "to": ["user@example.com"],
    "subject": "Welcome!",
    "body": {
      "type": "html",
      "content": "<p>Thanks for signing up.</p>"
    }
  }
  ```

### SMS request (`messages.sms.request`)

- **Kafka key:** `message_id`
- **Fields:**

  | Field | Required | Type | Constraints |
  |-------|----------|------|-------------|
  | `message_id`, `tenant_id`, `trace_id`, `created_at`, `meta` | As above | — | `channel` must be `sms` |
  | `from` | Yes | string | E.164 formatted number |
  | `to` | Yes | array[string] | 1–10 E.164 numbers |
  | `body.type` | No | string | Defaults to `text`; only `text` permitted |
  | `body.content` | Yes | string | ≤1600 runes |

- **Example:**

  ```json
  {
    "message_id": "49787c3d-5ef1-4b31-88a1-0a42e53e1fb3",
    "channel": "sms",
    "created_at": "2025-10-16T09:31:15Z",
    "from": "+15551234567",
    "to": ["+15557654321"],
    "body": {
      "type": "text",
      "content": "Your one-time code is 123456"
    }
  }
  ```

### WhatsApp request (`messages.whatsapp.request`)

- **Kafka key:** `message_id`
- **Fields:**

  | Field | Required | Type | Constraints |
  |-------|----------|------|-------------|
  | `message_id`, `tenant_id`, `trace_id`, `created_at`, `meta` | As above | — | `channel` must be `whatsapp` |
  | `from` | Yes | string | E.164 formatted number approved for WhatsApp |
  | `to` | Yes | array[string] | 1–100 E.164 numbers |
  | `body.type` | No | string | Defaults to `text`; allowed values: `text`, `template`, `media` |
  | `body.content` | Yes | string | ≤4096 bytes |
  | `body.media_type` | Conditional | string | Required when `body.type` is `media`; MIME type (e.g., `image/png`) |

- **Example:**

  ```json
  {
    "message_id": "6f731cf2-f5bc-4d3f-9d6d-5a0cd9b2e3ab",
    "channel": "whatsapp",
    "created_at": "2025-10-16T09:32:05Z",
    "from": "+15559876543",
    "to": ["+15557654321"],
    "body": {
      "type": "template",
      "content": "order_update"
    },
    "meta": {
      "order_id": "A12345"
    }
  }
  ```

### Status events (`messages.<channel>.status`)

- **Kafka key:** `message_id`
- **Fields:**

  | Field | Required | Type | Constraints |
  |-------|----------|------|-------------|
  | `message_id` | Yes | string | Matches originating request |
  | `channel` | Yes | string | `email`, `sms`, or `whatsapp` |
  | `event_type` | Yes | string | One of `queued`, `attempt`, `sent`, `failed` (future values reserved) |
  | `attempt` | No | integer | ≥0; >0 for retry attempts |
  | `provider_response.status` | No | string | Adapter-normalised status (`ok`, `rejected`, `rate_limited`, `unknown`, …) |
  | `provider_response.code` | No | integer | Provider response/status code |
  | `provider_response.message` | No | string | Provider description |
  | `provider_response.raw` | No | string | Provider payload truncated to ≤1024 runes |
  | `provider_response.meta` | No | object | Provider-specific string map |
  | `error` | No | string | Present for failure scenarios |
  | `trace_id` | No | string | Propagated trace identifier |
  | `timestamp` | Yes | string | RFC3339 timestamp set by the worker |

- **Example:**

  ```json
  {
    "message_id": "49787c3d-5ef1-4b31-88a1-0a42e53e1fb3",
    "channel": "sms",
    "event_type": "failed",
    "attempt": 3,
    "provider_response": {
      "status": "rate_limited",
      "code": 429,
      "message": "Twilio 20429: Rate limit exceeded"
    },
    "error": "transient error: http 429",
    "trace_id": "trace-123",
    "timestamp": "2025-10-16T09:35:45.123Z"
  }
  ```

### DLQ records (`messages.<channel>.dlq`)

- **Kafka key:** `message_id`
- **Fields:**

  | Field | Required | Type | Constraints |
  |-------|----------|------|-------------|
  | `message_id` | Yes | string | Matches originating request |
  | `channel` | Yes | string | `email`, `sms`, or `whatsapp` |
  | `original_message` | Yes | object/string | Original payload as JSON when valid; otherwise base64 string |
  | `attempts` | Yes | integer | ≥1 |
  | `failure_type` | Yes | string | `permanent`, `transient`, `validation`, or `unknown` |
  | `last_error` | No | string | Final error emitted by adapter/engine |
  | `first_failed_at` | Yes | string | Timestamp of first failure |
  | `last_attempt_at` | Yes | string | Timestamp of final attempt |
  | `trace_id` | No | string | Copied from the request |
  | `meta` | No | object | String map derived from validated metadata |

- **Example:**

  ```json
  {
    "message_id": "1e8f3b1b-4d0d-4bd3-9c86-2f72c7808c61",
    "channel": "email",
    "original_message": {
      "message_id": "1e8f3b1b-4d0d-4bd3-9c86-2f72c7808c61",
      "channel": "email",
      "created_at": "2025-10-16T09:30:00Z",
      "from": "noreply@example.com",
      "to": ["user@example.com"],
      "body": {
        "type": "html",
        "content": "<p>Thanks for signing up.</p>"
      }
    },
    "attempts": 3,
    "failure_type": "permanent",
    "last_error": "permanent error: smtp 550 recipient rejected",
    "first_failed_at": "2025-10-16T09:31:00Z",
    "last_attempt_at": "2025-10-16T09:33:10Z",
    "trace_id": "trace-123",
    "meta": {
      "campaign": "autumn-cta"
    }
  }
  ```

---

## Project layout

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

`internal/health` is intentionally empty today; health endpoints are left for future work (see “Known gaps” in the design doc).

---

## Prerequisites

- Go 1.22+
- Docker & Docker Compose (for local Kafka cluster)
- Make, Bash, and GNU coreutils (scripts assume these are available)

---

## Getting started

1. **Clone & configure**
   ```bash
   cp .env.example .env
   # customise provider selections, credentials, and topic names as needed
   ```

2. **Start Kafka (optional but recommended)**
   ```bash
   docker compose up -d kafka-1 kafka-2 kafka-3
   docker compose run --rm kafka-init      # creates topics/quotas defined in scripts/kafka-init.sh
   docker compose up -d kafka-ui           # optional UI at http://localhost:28080
   ```

3. **Run a worker locally**
   ```bash
   go run ./cmd/email-worker     # similarly ./cmd/sms-worker or ./cmd/whatsapp-worker
   ```
   Workers honour `KAFKA_BROKERS`, channel topics, and provider selections from the environment.

4. **Produce sample traffic**
   ```bash
   scripts/email-scenario-producer.sh --help
   scripts/email-scenario-producer.sh --scenario transient   # emits requests with metadata hints for mock providers
   scripts/check-kafka-connection.sh                        # verifies broker connectivity via KAFKA_TEMP_TOPIC
   ```

5. **Run tests**
   ```bash
   go test ./...
   ```
   Targeted suites live in `test/` (config parsing, validation helpers, worker engine).

---

## Configuration reference

Key environment variables (default when omitted shown in parentheses):

| Variable | Description |
|----------|-------------|
| `APP_ENV` (`development`) | Controls logging format (console in dev, JSON otherwise). |
| `APP_PORT` (`8080`) | Reserved for future HTTP health server. |
| `LOG_LEVEL` (`info`) | Global zerolog level. |
| `KAFKA_BROKERS` (required) | Comma-separated broker list. |
| `KAFKA_*_REQUEST_TOPIC` (required) | Request topics per channel. |
| `KAFKA_*_STATUS_TOPIC` (required) | Status topics per channel. |
| `KAFKA_*_DLQ_TOPIC` (required) | DLQ topics per channel. |
| `EMAIL_CONSUMER_GROUP` etc. (required) | Consumer group IDs per channel. |
| `MAX_ATTEMPTS` (`3`) | Retry budget per message. |
| `BASE_BACKOFF_SECONDS` (`10`), `MAX_BACKOFF_SECONDS` (`120`) | Exponential backoff window. |
| `WORKER_CONCURRENCY` (`10`) | Maximum in-flight messages per worker process. |
| `COMMIT_ON_SUCCESS_ONLY` (`true`) | Enables manual offset commits after successful processing. |
| Validation limits | `MSG_MAX_BYTES`, `RECIPIENTS_MAX`, `BODY_MAX_BYTES`, `SMS_BODY_MAX`, `WA_BODY_MAX`, metadata constraints. |
| `EMAIL_PROVIDER` (`mock`) | `mock` or `smtp`; SMTP credentials required when `smtp`. |
| `SMS_PROVIDER` (`mock`), `WHATSAPP_PROVIDER` (`mock`) | `mock` or `twilio`; Twilio credentials required when `twilio`. |
| `PROVIDER_TIMEOUT_SECONDS` (`30`) | Captured for future provider timeouts (currently informational). |
| `HEALTH_ENABLE_PROVIDER_PROBE` (`false`) | Placeholder for future health probing. |

Refer to `.env.example` for a complete list tuned for a multi-broker dev cluster (replication factor 3, higher validation limits, five retry attempts).

---

## Topics, events, and DLQ payloads

- **Request topics**: `messages.email.request`, `messages.sms.request`, `messages.whatsapp.request`. Message keys should be `message_id` to keep lifecycle events co-partitioned.
- **Status events** (`models.StatusEvent`): `queued`, `attempt`, `sent`, `failed` today, with room for `rejected`, `rate_limited`, `dlq`. Payload includes attempt count, provider response metadata, error text, and timestamp.
- **DLQ records** (`models.DLQRecord`): contain the original payload (JSON or base64), attempt counts, failure type (`permanent|transient|validation|unknown`), trace ID, and metadata snapshot for replay tooling.

Status and DLQ publishers live in `internal/kafka/publisher` and use synchronous Kafka writes to guarantee durability before offsets are committed.

---

## Running in containers

`docker-compose.yml` provisions:

- Three-node KRaft Kafka cluster (`kafka-1`..`kafka-3`) with replication factor 3 and topic auto-create disabled.
- A one-shot `kafka-init` job wires topics and quotas via `scripts/kafka-init.sh`.
- Optional `kafka-ui` at `http://localhost:28080`.
- Channel worker services (builds from the local Dockerfile) that can be enabled once `.env` is configured.

To bring up workers alongside Kafka:

```bash
docker compose up --build email-worker sms-worker whatsapp-worker
```

Ensure secrets are injected securely when running outside development (see the “Security” section in `docs/design_initial.md`).

---

## Additional scripts

- `scripts/email-scenario-producer.sh`: sends scenario-driven requests (supports transient/permanent failure simulations).
- `scripts/kafka-producer-load-test.sh`: high-volume synthetic load generator for soak testing.
- `scripts/check-kafka-connection.sh`: connectivity probe using `KAFKA_TEMP_TOPIC`.

Scripts automatically source `.env` (or a provided `--env-file`) and defer to CLI overrides.

---

## References

- Implementation design: `docs/design.md`
- Original detailed specification: `docs/design_initial.md`
- Environment template: `.env.example`
- Provider behaviour: `internal/providers/` (SMTP, Twilio, mock implementations)
- Worker engine tests: `test/worker/engine_test.go`

For questions or extension ideas, start by reviewing the “Known gaps” section in `docs/design.md` — it lists pending improvements around health checks, telemetry, and timeout enforcement.
