#!/usr/bin/env bash
set -euo pipefail

# Produces a sample email request to Kafka using docker-compose.
# Usage: scripts/produce-sample-email.sh [recipient-email]

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RECIPIENT="${1:-ajay@quantiqueminds.com}"
TOPIC="${KAFKA_EMAIL_TOPIC:-messages.email.request}"
BOOTSTRAP="${KAFKA_BOOTSTRAP:-localhost:9092}"
FROM_ADDRESS="${SMTP_FROM:-noreply@quantiqueminds.com}"

if ! command -v uuidgen >/dev/null 2>&1; then
  echo "uuidgen not found on PATH. Please install uuidgen or provide UUID via PAYLOAD_MESSAGE_ID." >&2
  exit 1
fi

MESSAGE_ID="${PAYLOAD_MESSAGE_ID:-$(uuidgen)}"
CREATED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

PAYLOAD=$(cat <<EOF
{
  "message_id": "$MESSAGE_ID",
  "channel": "email",
  "created_at": "$CREATED_AT",
  "from": "$FROM_ADDRESS",
  "to": ["$RECIPIENT"],
  "subject": "Adapters/worker integration test",
  "body": {
    "type": "text",
    "content": "Hello from the messaging service harness.\n\nThis email was published at $CREATED_AT.\n"
  },
  "meta": {
    "source": "local-test",
    "trace": "$MESSAGE_ID"
  }
}
EOF
)

if command -v jq >/dev/null 2>&1; then
  PAYLOAD_COMPACT=$(printf '%s' "$PAYLOAD" | jq -c '.')
else
  PAYLOAD_COMPACT=$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)))' <<<"$PAYLOAD")
fi

echo "Publishing payload to topic $TOPIC on $BOOTSTRAP"
printf '%s\n' "$PAYLOAD_COMPACT"

printf '%s\n' "$PAYLOAD_COMPACT" | docker compose exec -T broker sh -c "/opt/kafka/bin/kafka-console-producer.sh --bootstrap-server $BOOTSTRAP --topic $TOPIC"

echo "Message queued with id $MESSAGE_ID"
