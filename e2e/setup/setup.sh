#!/usr/bin/env bash
set -aeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Running setup.sh"

if [ -z "${SCYLLADB_CLOUD_TOKEN:-}" ]; then
  echo "SCYLLADB_CLOUD_TOKEN must be set to a ScyllaDB Cloud API token" >&2
  exit 1
fi
SCYLLADB_CLOUD_ENDPOINT="${SCYLLADB_CLOUD_ENDPOINT:-https://api.cloud.scylladb.com}"

echo "Creating provider credentials secret..."
${KUBECTL} -n crossplane-system create secret generic provider-secret \
  --from-literal=credentials="{\"token\":\"${SCYLLADB_CLOUD_TOKEN}\",\"endpoint\":\"${SCYLLADB_CLOUD_ENDPOINT}\"}" \
  --dry-run=client -o yaml | ${KUBECTL} apply -f -

echo "Waiting until provider is healthy..."
${KUBECTL} wait provider.pkg --all --for condition=Healthy --timeout 5m

echo "Waiting for all pods to come online..."
${KUBECTL} -n crossplane-system wait --for=condition=Available deployment --all --timeout=5m

echo "Creating default (cluster)provider configs..."
${KUBECTL} apply -f "${SCRIPT_DIR}/providerconfigs.yaml"
