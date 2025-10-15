#!/bin/bash
set -euo pipefail

# Ensure Kafka CLI utilities are on PATH
export PATH=/opt/bitnami/kafka/bin:$PATH

echo "Waiting for cluster..."
while true; do
  if kafka-broker-api-versions.sh --bootstrap-server kafka-1:9092 >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

echo "Creating topics with final retention policies..."
for ch in email sms whatsapp; do
  # Request: keep everything forever (no time deletion)
  kafka-topics.sh --create --if-not-exists \
    --bootstrap-server kafka-1:9092 \
    --topic "messages.${ch}.request" \
    --partitions 6 --replication-factor 3 \
    --config min.insync.replicas=2 \
    --config cleanup.policy=delete \
    --config retention.ms=-1

  # Status: compact + no time deletion (keeps latest per key; never time-purges)
  kafka-topics.sh --create --if-not-exists \
    --bootstrap-server kafka-1:9092 \
    --topic "messages.${ch}.status" \
    --partitions 6 --replication-factor 3 \
    --config min.insync.replicas=2 \
    --config cleanup.policy=compact,delete \
    --config retention.ms=-1

  # DLQ: keep everything forever
  kafka-topics.sh --create --if-not-exists \
    --bootstrap-server kafka-1:9092 \
    --topic "messages.${ch}.dlq" \
    --partitions 6 --replication-factor 3 \
    --config min.insync.replicas=2 \
    --config cleanup.policy=delete \
    --config retention.ms=-1
done

echo "Applying client quotas (defaults)..."
# Default per-client quotas: 10 MB/s produce + consume
kafka-configs.sh --alter --bootstrap-server kafka-1:9092 \
  --entity-type clients --entity-default \
  --add-config producer_byte_rate=10485760,consumer_byte_rate=10485760

echo "Optional per-client overrides (uncomment and adjust client-ids):"
# kafka-configs.sh --alter --bootstrap-server kafka-1:9092 \
#   --entity-type clients --entity-name svc-email-worker \
#   --add-config producer_byte_rate=20971520,consumer_byte_rate=10485760
# kafka-configs.sh --alter --bootstrap-server kafka-1:9092 \
#   --entity-type clients --entity-name svc-sms-worker \
#   --add-config producer_byte_rate=10485760,consumer_byte_rate=10485760

echo "Replication bandwidth guardrails per broker (100 MB/s):"
for id in 1 2 3; do
  kafka-configs.sh --alter --bootstrap-server kafka-1:9092 \
    --entity-type brokers --entity-name "$id" \
    --add-config follower.replication.throttled.rate=100000000,leader.replication.throttled.rate=100000000
done

echo "All done."
