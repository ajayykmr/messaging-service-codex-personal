#!/usr/bin/env bash
set -euo pipefail

# Publishes email request scenarios to Kafka for positive and negative testing.
# Scenarios mirror validation rules in docs/design.md and exercise the mock
# provider retry/latency headers defined in internal/providers/email/mock_provider.go.
#
# Usage:
#   scripts/email-scenario-producer.sh --scenario happy-path
#   scripts/email-scenario-producer.sh --all
#   scripts/email-scenario-producer.sh --scenario retry-two-attempts --count 3
# Environment overrides:
#   KAFKA_BOOTSTRAP        - bootstrap servers (default: KAFKA_BROKERS or kafka-1:9092)
#   KAFKA_EMAIL_TOPIC      - email request topic (default messages.email.request)
#   KAFKA_BROKER_SERVICE   - docker compose service name (default kafka-1)
#   SMTP_FROM              - default From address (default noreply@quantiqueminds.com)
#   EMAIL_RECIPIENT        - primary recipient (default ajay@quantiqueminds.com)
#   PRODUCER_VERBOSE       - set to 1 for verbose logging
#   ENV_FILE               - optional env file to source before loading overrides (.env default)

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-.env}"
if [[ -f "$ENV_FILE" ]]; then
  echo "==> Loading environment from $ENV_FILE"
  set -a
  # shellcheck source=/dev/null
  source "$ENV_FILE"
  set +a
fi

BOOTSTRAP="${KAFKA_BOOTSTRAP:-${KAFKA_BROKERS:-kafka-1:9092}}"
TOPIC="${KAFKA_EMAIL_TOPIC:-messages.email.request}"
BROKER_SERVICE="${KAFKA_BROKER_SERVICE:-kafka-1}"
DEFAULT_FROM="${SMTP_FROM:-noreply@quantiqueminds.com}"
DEFAULT_RECIPIENT="${EMAIL_RECIPIENT:-ajay@quantiqueminds.com}"
PRODUCER_VERBOSE="${PRODUCER_VERBOSE:-0}"

SCENARIOS=()
ITERATIONS=1

function usage() {
  cat <<'EOF'
Usage: email-scenario-producer.sh [--scenario NAME] [--all] [--count N] [--dry-run]

Scenarios:
  happy-path             - Valid email request.
  invalid-json           - Malformed JSON payload (parser failure).
  invalid-from           - Invalid from address (validation failure).
  channel-mismatch       - Channel set to sms (worker rejects).
  missing-created-at     - Created_at omitted (validation failure).
  oversize-body          - Body exceeds MSG_MAX_BYTES (validation failure).
  permanent-failure      - Provider header forces permanent failure.
  transient-retry        - Provider header forces transient failure/DLQ.
  retry-two-attempts     - Header triggers two transient failures then success.
  provider-timeout       - Provider header forces timeout.
  custom                  - Reads payload from STDIN (optional headers via env CUSTOM_HEADERS).

Options:
  --scenario NAME        - Run a single scenario (can be specified multiple times).
  --all                  - Run all predefined scenarios.
  --count N              - Produce the scenario N times (default 1).
  --dry-run              - Print payloads instead of sending to Kafka.
  --help                 - Show this message.

Environment overrides:
  CUSTOM_HEADERS         - Comma separated header list for custom scenario.
EOF
}

DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario)
      SCENARIOS+=("$2")
      shift 2
      ;;
    --all)
      SCENARIOS=(happy-path invalid-json invalid-from channel-mismatch missing-created-at oversize-body permanent-failure transient-retry retry-two-attempts provider-timeout)
      shift
      ;;
    --count)
      ITERATIONS="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ ${#SCENARIOS[@]} -eq 0 ]]; then
  SCENARIOS=(happy-path)
fi

if ! command -v uuidgen >/dev/null 2>&1; then
  echo "uuidgen is required. Install uuidgen or export PAYLOAD_MESSAGE_ID manually." >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required for payload transformations. Please install jq." >&2
  exit 1
fi

KAFKA_BIN="/opt/bitnami/kafka/bin"

function run_in_broker() {
  local cmd="$1"
  docker compose exec -T "$BROKER_SERVICE" sh -c "$cmd"
}

function json_compact() {
  if command -v jq >/dev/null 2>&1; then
    jq -c '.'
  else
    python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)))'
  fi
}

function oversized_body() {
  local max_bytes="${MSG_MAX_BYTES:-200000}"
  if ! [[ "$max_bytes" =~ ^[0-9]+$ ]]; then
    echo "Warning: MSG_MAX_BYTES='$max_bytes' is not numeric. Falling back to 200000." >&2
    max_bytes=200000
  fi
  python3 - "$max_bytes" <<'PY'
import sys
limit = int(sys.argv[1])
sys.stdout.write("A" * (limit + 1))
PY
}

function base_payload() {
  local message_id="$1"
  local created_at
  created_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  cat <<EOF
{
  "message_id": "$message_id",
  "channel": "email",
  "created_at": "$created_at",
  "from": "$DEFAULT_FROM",
  "to": ["$DEFAULT_RECIPIENT"],
  "subject": "Adapters/worker integration test",
  "body": {
    "type": "text",
    "content": "Hello from the messaging service harness.\\n"
  },
  "meta": {
    "source": "scenario-runner",
    "trace": "$message_id"
  }
}
EOF
}

function scenario_payload() {
  local scenario="$1"
  local message_id="$2"

  case "$scenario" in
    happy-path)
      base_payload "$message_id" | json_compact
      ;;
    invalid-json)
      printf '{"message_id": "%s", "channel": "email"\n' "$message_id"
      ;;
    invalid-from)
      base_payload "$message_id" | jq '.from = "not-an-email"'
      ;;
    channel-mismatch)
      base_payload "$message_id" | jq '.channel = "sms"'
      ;;
    missing-created-at)
      base_payload "$message_id" | jq 'del(.created_at)'
      ;;
    oversize-body)
      base_payload "$message_id" | jq --rawfile content <(oversized_body) '.body.content = $content'
      ;;
    permanent-failure)
      base_payload "$message_id" | jq '.meta.provider_scenario = "permanent"'
      ;;
    transient-retry)
      base_payload "$message_id" | jq '.meta.provider_scenario = "transient"'
      ;;
    retry-two-attempts)
      base_payload "$message_id"
      ;;
    provider-timeout)
      base_payload "$message_id" | jq '.meta.provider_scenario = "timeout"'
      ;;
    custom)
      cat
      ;;
    *)
      echo "Unknown scenario: $scenario" >&2
      exit 1
      ;;
  esac
}

function scenario_headers() {
  local scenario="$1"
  case "$scenario" in
    permanent-failure)
      echo "X-Mock-Provider-Scenario:permanent"
      ;;
    transient-retry)
      echo "X-Mock-Provider-Scenario:transient"
      ;;
    retry-two-attempts)
      echo "X-Mock-Provider-Failure-Attempts:2"
      ;;
    provider-timeout)
      echo "X-Mock-Provider-Scenario:timeout,X-Mock-Provider-Latency:2s"
      ;;
    custom)
      echo "${CUSTOM_HEADERS:-}"
      ;;
    *)
      echo ""
      ;;
  esac
}

function send_to_kafka() {
  local payload="$1"
  local headers="$2"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "--- DRY RUN ---"
    [[ -n "$headers" ]] && echo "Headers: $headers"
    echo "$payload"
    return
  fi

  local cmd="$KAFKA_BIN/kafka-console-producer.sh --bootstrap-server \"$BOOTSTRAP\" --topic \"$TOPIC\""
  if [[ -n "$headers" ]]; then
    cmd+=" --property parse.headers=true"
    [[ "$PRODUCER_VERBOSE" == "1" ]] && echo "Sending with headers: $headers"
    printf '%s\t%s\n' "$headers" "$payload" | run_in_broker "$cmd"
  else
    [[ "$PRODUCER_VERBOSE" == "1" ]] && echo "Sending without headers"
    printf '%s\n' "$payload" | run_in_broker "$cmd"
  fi
}

for scenario in "${SCENARIOS[@]}"; do
  for ((i = 1; i <= ITERATIONS; i++)); do
    MESSAGE_ID="${PAYLOAD_MESSAGE_ID:-$(uuidgen)}"

    if [[ "$scenario" == "custom" ]]; then
      echo "Reading custom payload from STDIN (press Ctrl+D when done)..."
    fi

    PAYLOAD="$(scenario_payload "$scenario" "$MESSAGE_ID")"

    # compact JSON except for invalid-json which is intentionally malformed
    if [[ "$scenario" != "invalid-json" && "$scenario" != "custom" ]]; then
      PAYLOAD="$(printf '%s\n' "$PAYLOAD" | json_compact)"
    fi

    HEADERS="$(scenario_headers "$scenario")"

    echo "==> Scenario: $scenario (iteration $i/$ITERATIONS)"
    echo "Topic: $TOPIC"
    echo "Bootstrap: $BOOTSTRAP"
    echo "Message ID: $MESSAGE_ID"
    [[ -n "$HEADERS" ]] && echo "Headers: $HEADERS"

    send_to_kafka "$PAYLOAD" "$HEADERS"
    echo
  done
done

echo "Scenario publishing complete."
