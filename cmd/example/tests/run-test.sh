#!/bin/bash

# Load environment variables
if [ -f .env ]; then
    export $(cat .env | grep -v '^#' | xargs)
fi

# Default values
BASE_URL="${BASE_URL:-http://host.containers.internal:8080}"
K6_PROMETHEUS_RW_SERVER_URL="${K6_PROMETHEUS_RW_SERVER_URL:-http://host.containers.internal:9090/api/v1/write}"
K6_IMAGE="${K6_IMAGE:-grafana/k6:latest}"

# Get test file from argument
TEST_FILE="${1:-load-test.js}"

if [ ! -f "k6/${TEST_FILE}" ]; then
    echo "Error: Test file k6/${TEST_FILE} not found"
    exit 1
fi

echo "Running test: ${TEST_FILE}"
echo "Base URL: ${BASE_URL}"
echo ""

mkdir -p results

podman run --rm \
    --name k6-test \
    --add-host=host.containers.internal:host-gateway \
    -v "$(pwd)/k6:/scripts:ro,Z" \
    -v "$(pwd)/results:/results:Z" \
    -e BASE_URL="${BASE_URL}" \
    -e K6_PROMETHEUS_RW_SERVER_URL="${K6_PROMETHEUS_RW_SERVER_URL}" \
    -e K6_PROMETHEUS_RW_TREND_AS_NATIVE_HISTOGRAM="${K6_PROMETHEUS_RW_TREND_AS_NATIVE_HISTOGRAM:-true}" \
    -e K6_PROMETHEUS_RW_PUSH_INTERVAL="${K6_PROMETHEUS_RW_PUSH_INTERVAL:-10s}" \
    -e K6_PROMETHEUS_RW_TAGS_ENV="${K6_PROMETHEUS_RW_TAGS_ENV:-development}" \
    -e K6_PROMETHEUS_RW_TAGS_SERVICE="${K6_PROMETHEUS_RW_TAGS_SERVICE:-gath-web}" \
    "${K6_IMAGE}" \
    run \
    --out json="/results/$(basename ${TEST_FILE} .js)-$(date +%Y%m%d-%H%M%S).json" \
    --out experimental-prometheus-rw \
    "/scripts/${TEST_FILE}"