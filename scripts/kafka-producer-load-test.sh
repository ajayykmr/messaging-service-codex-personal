#!/usr/bin/env bash
set -euo pipefail

# kafka-producer-load-test.sh
# ---------------------------
# Generates realistic email request payloads (matching the structure used by
# email-scenario-producer.sh) and pipes them into Kafka via the broker
# container. Useful for local load testing without relying on synthetic binary
# payloads from kafka-producer-perf-test.sh.
#
# Usage:
#   scripts/kafka-producer-load-test.sh \
#     --topic messages.email.request \
#     --num-records 200000 \
#     --rate 40000 \
#     --body-size 1024 \
#     --provider-scenario transient \
#     --meta client=test-harness
#
# Key options:
#   --num-records N         Total records to emit (default: 100000)
#   --rate R                Target records/sec (-1 for unlimited, default: 20000)
#   --subject TEXT          Email subject (default mirrors scenario runner)
#   --body TEXT             Email body template (default mirrors scenario runner)
#   --body-size BYTES       Override body length with fixed BYTES of 'A'
#   --from ADDRESS          Sender address (default SMTP_FROM/.env fallback)
#   --recipients LIST       Comma-separated recipient list (default EMAIL_RECIPIENT/.env)
#   --provider-scenario S   Optional provider scenario meta (permanent|transient|timeout)
#   --meta KEY=VALUE        Extra meta fields (repeatable)
#   --bootstrap SERVERS     Bootstrap override (default honours KAFKA_BOOTSTRAP/KAFKA_BROKERS)
#   --broker-service NAME   docker compose service name (default kafka-1)
#   --env-file PATH         Optional env file to source first (default .env when present)
#   --dry-run               Print sample payload and exit without sending
#   --help                  Display help text

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE=".env"
NUM_RECORDS=100000
RATE_LIMIT=20000
BODY_SIZE=-1
SUBJECT_OVERRIDE=""
BODY_OVERRIDE=""
FROM_OVERRIDE=""
RECIPIENTS_OVERRIDE=""
PROVIDER_SCENARIO=""
BOOTSTRAP_OVERRIDE=""
BROKER_SERVICE_OVERRIDE=""
TOPIC_OVERRIDE=""
DRY_RUN=0
declare -a META_OVERRIDES=()

function usage() {
  sed -n '9,40p' "$0"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --num-records)
      NUM_RECORDS="$2"
      shift 2
      ;;
    --rate)
      RATE_LIMIT="$2"
      shift 2
      ;;
    --subject)
      SUBJECT_OVERRIDE="$2"
      shift 2
      ;;
    --body)
      BODY_OVERRIDE="$2"
      shift 2
      ;;
    --body-size)
      BODY_SIZE="$2"
      shift 2
      ;;
    --topic)
      TOPIC_OVERRIDE="$2"
      shift 2
      ;;
    --from)
      FROM_OVERRIDE="$2"
      shift 2
      ;;
    --recipients)
      RECIPIENTS_OVERRIDE="$2"
      shift 2
      ;;
    --provider-scenario)
      PROVIDER_SCENARIO="$2"
      shift 2
      ;;
    --meta)
      META_OVERRIDES+=("$2")
      shift 2
      ;;
    --bootstrap)
      BOOTSTRAP_OVERRIDE="$2"
      shift 2
      ;;
    --broker-service)
      BROKER_SERVICE_OVERRIDE="$2"
      shift 2
      ;;
    --env-file)
      ENV_FILE="$2"
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
      echo "unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -f "$ENV_FILE" ]]; then
  echo "==> Loading environment from $ENV_FILE"
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

# Ensure META_OVERRIDES stays defined even if sourcing clobbered it.
if ! declare -p META_OVERRIDES >/dev/null 2>&1; then
  declare -a META_OVERRIDES=()
fi

if ! [[ "$NUM_RECORDS" =~ ^[0-9]+$ ]] || (( NUM_RECORDS <= 0 )); then
  echo "num-records must be a positive integer" >&2
  exit 1
fi

if ! [[ "$BODY_SIZE" =~ ^-?[0-9]+$ ]]; then
  echo "body-size must be an integer (use -1 to keep template length)" >&2
  exit 1
fi

if ! [[ "$RATE_LIMIT" =~ ^-?[0-9]+([.][0-9]+)?$ ]]; then
  echo "rate must be numeric (use -1 for unlimited)" >&2
  exit 1
fi

BOOTSTRAP="${BOOTSTRAP_OVERRIDE:-${KAFKA_BOOTSTRAP:-${KAFKA_BROKERS:-kafka-1:9092}}}"
BROKER_SERVICE="${BROKER_SERVICE_OVERRIDE:-${KAFKA_BROKER_SERVICE:-kafka-1}}"
DEFAULT_FROM="${FROM_OVERRIDE:-${SMTP_FROM:-noreply@quantiqueminds.com}}"
DEFAULT_RECIPIENTS="${RECIPIENTS_OVERRIDE:-${EMAIL_RECIPIENT:-ajay@quantiqueminds.com}}"
SUBJECT="${SUBJECT_OVERRIDE:-Adapters/worker integration test}"
BODY_TEMPLATE="${BODY_OVERRIDE:-Hello from the messaging service harness.\\n}"
TOPIC="${TOPIC_OVERRIDE:-${KAFKA_EMAIL_TOPIC:-messages.email.request}}"

for cmd in docker python3; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "required command '$cmd' not found on PATH" >&2
    exit 1
  fi
done

KAFKA_BIN="/opt/bitnami/kafka/bin"

PYTHON_GENERATOR=$(cat <<'PY'
import json
import sys
import time
import uuid
from datetime import datetime, timezone

def decode_escapes(value: str) -> str:
    return bytes(value, "utf-8").decode("unicode_escape")

def parse_recipients(raw: str, fallback: str) -> list[str]:
    entries = [part.strip() for part in raw.split(",") if part.strip()]
    return entries or [fallback]

def build_payload(message_id: str, from_addr: str, recipients: list[str], subject: str,
                  body_content: str, scenario: str, meta_overrides: dict[str, str]) -> str:
    created_at = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    meta = {"source": "load-test", "trace": message_id}
    if scenario:
        meta["provider_scenario"] = scenario
    meta.update(meta_overrides)
    payload = {
        "message_id": message_id,
        "channel": "email",
        "created_at": created_at,
        "from": from_addr,
        "to": recipients,
        "subject": subject,
        "body": {
            "type": "text",
            "content": body_content,
        },
        "meta": meta,
    }
    return json.dumps(payload, separators=(",", ":"))

def main() -> None:
    if len(sys.argv) < 10:
        sys.stderr.write("internal error: insufficient arguments supplied to generator\n")
        sys.exit(2)

    mode = sys.argv[1]
    num_records = int(sys.argv[2])
    body_size = int(sys.argv[3])
    subject = decode_escapes(sys.argv[4])
    body_template = decode_escapes(sys.argv[5])
    from_addr = sys.argv[6]
    recipients_raw = sys.argv[7]
    scenario = sys.argv[8].strip()
    rate = float(sys.argv[9])
    meta_pairs = sys.argv[10:]

    recipients = parse_recipients(recipients_raw, from_addr)
    meta_overrides = {}
    for pair in meta_pairs:
        if "=" not in pair:
            continue
        key, value = pair.split("=", 1)
        key = key.strip()
        if not key:
            continue
        meta_overrides[key] = value.strip()

    if body_size >= 0:
        body_content = "A" * body_size
    else:
        body_content = body_template

    if mode == "sample":
        message_id = str(uuid.uuid4())
        print(build_payload(message_id, from_addr, recipients, subject, body_content, scenario, meta_overrides))
        return

    if mode != "generate":
        sys.stderr.write(f"unknown generator mode: {mode}\n")
        sys.exit(2)

    interval = None
    if rate > 0:
        interval = 1.0 / rate
    start = time.perf_counter()

    try:
        for index in range(num_records):
            message_id = str(uuid.uuid4())
            sys.stdout.write(build_payload(message_id, from_addr, recipients, subject, body_content, scenario, meta_overrides))
            sys.stdout.write("\n")
            if interval is not None:
                target = start + (index + 1) * interval
                while True:
                    remaining = target - time.perf_counter()
                    if remaining <= 0:
                        break
                    time.sleep(min(remaining, 0.05))
    except BrokenPipeError:
        # Downstream command failed; exit gracefully so the pipe failure surfaces in bash.
        return

if __name__ == "__main__":
    main()
PY
)

PY_ARGS=(
  "$NUM_RECORDS"
  "$BODY_SIZE"
  "$SUBJECT"
  "$BODY_TEMPLATE"
  "$DEFAULT_FROM"
  "$DEFAULT_RECIPIENTS"
  "$PROVIDER_SCENARIO"
  "$RATE_LIMIT"
)
if declare -p META_OVERRIDES >/dev/null 2>&1 && ((${#META_OVERRIDES[@]})); then
  PY_ARGS+=("${META_OVERRIDES[@]}")
fi

SAMPLE_PAYLOAD=$(python3 -c "$PYTHON_GENERATOR" sample "${PY_ARGS[@]}")
payload_bytes=$(printf '%s' "$SAMPLE_PAYLOAD" | LC_ALL=C wc -c | awk '{print $1}')

echo "==> Broker service: $BROKER_SERVICE"
echo "==> Bootstrap:      $BOOTSTRAP"
echo "==> Topic:          $TOPIC"
echo "==> Records:        $NUM_RECORDS"
TARGET_LABEL=$(python3 - "$RATE_LIMIT" <<'PY'
import sys
value = float(sys.argv[1])
if value > 0:
    if value.is_integer():
        print(f"{int(value)} records/sec")
    else:
        print(f"{value} records/sec")
else:
    print("unlimited")
PY
)
echo "==> Target rate:    $TARGET_LABEL"
echo "==> Sample payload (${payload_bytes} bytes):"
printf '%s\n' "$SAMPLE_PAYLOAD"

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "==> Dry run requested; skipping send."
  exit 0
fi

start_ts=$(python3 - <<'PY'
import time
print(f"{time.time():.6f}")
PY
)

if ! PYTHONUNBUFFERED=1 python3 -c "$PYTHON_GENERATOR" generate "${PY_ARGS[@]}" | \
  docker compose exec -T "$BROKER_SERVICE" "$KAFKA_BIN/kafka-console-producer.sh" \
    --bootstrap-server "$BOOTSTRAP" \
    --topic "$TOPIC"; then
  echo "✖ Load test failed to publish messages." >&2
  exit 1
fi

end_ts=$(python3 - <<'PY'
import time
print(f"{time.time():.6f}")
PY
)

elapsed=$(python3 - "$start_ts" "$end_ts" <<'PY'
import sys
start = float(sys.argv[1])
end = float(sys.argv[2])
delta = max(end - start, 1e-9)
print(f"{delta:.3f}")
PY
)

# Include newline written per record by the generator.
payload_line_bytes=$((payload_bytes + 1))
total_bytes=$((payload_line_bytes * NUM_RECORDS))

throughput=$(python3 - "$NUM_RECORDS" "$elapsed" <<'PY'
import sys
num = float(sys.argv[1])
elapsed = float(sys.argv[2])
print(f"{num/elapsed:.2f}")
PY
)

total_mib=$(python3 - "$total_bytes" <<'PY'
import sys
total = float(sys.argv[1])
print(f"{total / (1024 * 1024):.2f}")
PY
)

mib_per_sec=$(python3 - "$total_bytes" "$elapsed" <<'PY'
import sys
total = float(sys.argv[1])
elapsed = float(sys.argv[2])
print(f"{(total / (1024 * 1024)) / elapsed:.2f}")
PY
)

echo "==> Sent $NUM_RECORDS records (~${total_mib} MiB) in ${elapsed}s."
echo "==> Effective throughput: ${throughput} records/sec, ${mib_per_sec} MiB/sec."
