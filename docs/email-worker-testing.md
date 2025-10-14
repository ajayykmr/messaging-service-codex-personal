# Email Worker Testing & Negative Scenarios

This note captures the commands we’ve been using to exercise the end-to-end email worker locally, including the negative-test payloads supported by the helper script.

## Prerequisites

- Kafka broker running via `docker compose up -d`
- Required topics created (default names shown here):
  ```bash
  docker compose exec broker /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists --topic messages.email.request --partitions 6 --replication-factor 1

  docker compose exec broker /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists --topic messages.email.status --partitions 3 --replication-factor 1

  docker compose exec broker /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists --topic messages.email.dlq --partitions 3 --replication-factor 1
  ```
- `.env` populated so the worker can reach Kafka and SMTP (ensure `SMTP_FROM` matches a verified sender for your SMTP account).

## Run Tests

```bash
go test ./...
```

## Publish Sample Email Requests

The helper script supports multiple scenarios. Default recipient is `ajay@quantiqueminds.com`; override by passing an address as the first argument. Each scenario prints the payload and pipes it to Kafka via the broker container.

```bash
# Happy-path payload
scripts/produce-sample-email.sh --scenario success

# Malformed JSON (truncated body)
scripts/produce-sample-email.sh --scenario invalid-json

# Invalid sender address
scripts/produce-sample-email.sh --scenario invalid-email

# Channel mismatch (uses "sms" instead of "email")
scripts/produce-sample-email.sh --scenario channel-mismatch

# Missing created_at field
scripts/produce-sample-email.sh --scenario missing-created-at

# Body exceeds configured byte limit (200001 chars)
scripts/produce-sample-email.sh --scenario oversize-body
```

Each invocation emits:

```
Scenario: <scenario name>
Publishing payload to topic messages.email.request on localhost:9092
<JSON payload>
Message queued with id <UUID>
```

Use the worker logs (and, if needed, the status/DLQ topics) to verify the corresponding behaviour:

```bash
docker compose exec broker /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic messages.email.status --from-beginning

docker compose exec broker /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic messages.email.dlq --from-beginning
```

## Worker Startup Reminder

```bash
source .env
go run cmd/email-worker/main.go
```

You should see:

```
INF email worker started request_topic=messages.email.request service=email-worker
INF kafka consumer group ready component=consumer group_id=<group> service=email-worker
```

Keep this worker running while you publish messages so it can process and emit status/DLQ events.
