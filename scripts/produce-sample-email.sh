#!/usr/bin/env bash
set -euo pipefail

# Produces a sample email request to Kafka using docker-compose.
# Usage:
#   scripts/produce-sample-email.sh [recipient-email] [--scenario SCENARIO]
# Supported scenarios: success (default), invalid-json, invalid-email,
# channel-mismatch, missing-created-at, oversize-body.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RECIPIENT="ajay@quantiqueminds.com"
SCENARIO="${SCENARIO:-success}"

if [[ $# -gt 0 && $1 != --* ]]; then
  RECIPIENT="$1"
  shift
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario)
      SCENARIO="${2:-}"
      if [[ -z "$SCENARIO" ]]; then
        echo "missing value for --scenario" >&2
        exit 1
      fi
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

TOPIC="${KAFKA_EMAIL_TOPIC:-messages.email.request}"
BOOTSTRAP="${KAFKA_BOOTSTRAP:-localhost:9092}"
FROM_ADDRESS="${SMTP_FROM:-noreply@quantiqueminds.com}"

if ! command -v uuidgen >/dev/null 2>&1; then
  echo "uuidgen not found on PATH. Please install uuidgen or provide UUID via PAYLOAD_MESSAGE_ID." >&2
  exit 1
fi

MESSAGE_ID="${PAYLOAD_MESSAGE_ID:-$(uuidgen)}"
CREATED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

PAYLOAD=""
COMPACT_JSON=true

case "$SCENARIO" in
  success)
    PAYLOAD=$(cat <<EOF_PAYLOAD
{
  "message_id": "$MESSAGE_ID",
  "channel": "email",
  "created_at": "$CREATED_AT",
  "from": "$FROM_ADDRESS",
  "to": ["$RECIPIENT"],
  "subject": "Adapters/worker integration test",
  "body": {
    "type": "text",
    "content": "Hello from the messaging service harness.\\n\\nThis email was published at $CREATED_AT.\\n"
  },
  "meta": {
    "source": "local-test",
    "trace": "$MESSAGE_ID"
  }
}
EOF_PAYLOAD
)
    ;;
  invalid-json)
    PAYLOAD="{\"message_id\": \"$MESSAGE_ID\", \"channel\": \"email\""
    COMPACT_JSON=false
    ;;
  invalid-email)
    PAYLOAD=$(cat <<EOF_PAYLOAD
{
  "message_id": "$MESSAGE_ID",
  "channel": "email",
  "created_at": "$CREATED_AT",
  "from": "invalid-address",
  "to": ["$RECIPIENT"],
  "subject": "Negative test - invalid email",
  "body": {
    "type": "text",
    "content": "This payload has an invalid from address."
  }
}
EOF_PAYLOAD
)
    ;;
  channel-mismatch)
    PAYLOAD=$(cat <<EOF_PAYLOAD
{
  "message_id": "$MESSAGE_ID",
  "channel": "sms",
  "created_at": "$CREATED_AT",
  "from": "$FROM_ADDRESS",
  "to": ["$RECIPIENT"],
  "subject": "Negative test - channel mismatch",
  "body": {
    "type": "text",
    "content": "The worker should reject this because the channel is not email."
  }
}
EOF_PAYLOAD
)
    ;;
  missing-created-at)
    PAYLOAD=$(cat <<EOF_PAYLOAD
{
  "message_id": "$MESSAGE_ID",
  "channel": "email",
  "from": "$FROM_ADDRESS",
  "to": ["$RECIPIENT"],
  "subject": "Negative test - missing created_at",
  "body": {
    "type": "text",
    "content": "created_at field intentionally omitted."
  }
}
EOF_PAYLOAD
)
    ;;
  oversize-body)
    OVERSIZED_CONTENT=$(python3 -c 'import sys; sys.stdout.write("A"*200001)')
    PAYLOAD=$(cat <<EOF_PAYLOAD
{
  "message_id": "$MESSAGE_ID",
  "channel": "email",
  "created_at": "$CREATED_AT",
  "from": "$FROM_ADDRESS",
  "to": ["$RECIPIENT"],
  "subject": "Negative test - oversize body",
  "body": {
    "type": "text",
    "content": "$OVERSIZED_CONTENT"
  }
}
EOF_PAYLOAD
)
    ;;
  *)
    echo "Unknown scenario: $SCENARIO" >&2
    exit 1
    ;;
esac

if [[ "$COMPACT_JSON" == true ]]; then
  if command -v jq >/dev/null 2>&1; then
    PAYLOAD=$(printf '%s' "$PAYLOAD" | jq -c '.')
  else
    PAYLOAD=$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)))' <<<"$PAYLOAD")
  fi
fi

echo "Scenario: $SCENARIO"
echo "Publishing payload to topic $TOPIC on $BOOTSTRAP"
printf '%s\n' "$PAYLOAD"

printf '%s\n' "$PAYLOAD" | docker compose exec -T broker sh -c "/opt/kafka/bin/kafka-console-producer.sh --bootstrap-server $BOOTSTRAP --topic $TOPIC"

echo "Message queued with id $MESSAGE_ID"
