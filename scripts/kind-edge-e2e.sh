#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KIND_BIN="${KIND_BIN:-kind}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
HELM_BIN="${HELM_BIN:-helm}"
DOCKER_BIN="${DOCKER_BIN:-docker}"
CLUSTER_NAME="${XISNOVE_KIND_CLUSTER_NAME:-xisnove-edge-e2e}"
NAMESPACE="${XISNOVE_KIND_NAMESPACE:-xisnove-edge-e2e}"
NODE_IMAGE="${XISNOVE_KIND_NODE_IMAGE:-kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f}"
RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$"
SERVER_IMAGE="${XISNOVE_KIND_E2E_SERVER_IMAGE:-xisnove-server:kind-e2e-${RUN_ID}}"
UI_IMAGE="${XISNOVE_KIND_E2E_UI_IMAGE:-xisnove-ui:kind-e2e-${RUN_ID}}"
OPERATOR_IMAGE="${XISNOVE_KIND_E2E_OPERATOR_IMAGE:-xisnove-operator:kind-e2e-${RUN_ID}}"
AGENT_IMAGE="${XISNOVE_KIND_E2E_AGENT_IMAGE:-xisnove-agent:kind-e2e-${RUN_ID}}"
SERVER_CONTAINER="xisnove-kind-e2e-control-plane-${RUN_ID}"
DATA_VOLUME="xisnove-kind-e2e-data-${RUN_ID}"
ARTIFACTS_DIR="${XISNOVE_KIND_ARTIFACTS_DIR:-${ROOT_DIR}/.artifacts/kind-edge-e2e/${RUN_ID}}"
TEMP_PARENT="${XISNOVE_KIND_TEMP_PARENT:-${ROOT_DIR}/.superpowers/tmp}"
mkdir -p "${TEMP_PARENT}"
# Colima shares the repository's /Users path but not macOS's per-user /private
# temp tree. Keep bind-mounted secret files under the repository-owned ignored
# state directory so a missing share cannot turn a file mount into a directory.
TEMP_DIR="$(mktemp -d "${TEMP_PARENT}/xisnove-kind-e2e.XXXXXX")"
KUBECONFIG_PATH="${TEMP_DIR}/kubeconfig"

export KUBECONFIG="${KUBECONFIG_PATH}"

require_command() {
  command -v "$1" >/dev/null 2>&1 || { echo "required command not found: $1" >&2; exit 1; }
}

collect_failure_evidence() {
  mkdir -p "${ARTIFACTS_DIR}"
  "${KIND_BIN}" export logs "${ARTIFACTS_DIR}/cluster" --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
  "${DOCKER_BIN}" logs "${SERVER_CONTAINER}" >"${ARTIFACTS_DIR}/control-plane.log" 2>&1 || true
  "${KUBECTL_BIN}" -n "${NAMESPACE}" get all,monitor,agent -o yaml >"${ARTIFACTS_DIR}/resources.yaml" 2>&1 || true
  # Secret values must never become CI artifacts. This template emits only
  # identity, type, and key names; it deliberately never dereferences values.
  "${KUBECTL_BIN}" -n "${NAMESPACE}" get secrets -o go-template='{{range .items}}{{.metadata.namespace}}/{{.metadata.name}} type={{.type}} keys={{range $key, $_ := .data}}{{$key}},{{end}}{{"\n"}}{{end}}' >"${ARTIFACTS_DIR}/secret-metadata.txt" 2>&1 || true
  if grep -En '(^|[[:space:]])(data|stringData):|"(data|stringData)"[[:space:]]*:' "${ARTIFACTS_DIR}/secret-metadata.txt" >/dev/null 2>&1; then
    echo "refusing to retain failure artifacts containing Secret values" >&2
    rm -f "${ARTIFACTS_DIR}/secret-metadata.txt"
  fi
  echo "kind failure evidence: ${ARTIFACTS_DIR}" >&2
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if (( status != 0 )); then collect_failure_evidence; fi
  if [[ "${XISNOVE_KIND_KEEP:-0}" != "1" ]]; then
    "${KIND_BIN}" delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
    "${DOCKER_BIN}" rm -f "${SERVER_CONTAINER}" >/dev/null 2>&1 || true
    "${DOCKER_BIN}" volume rm "${DATA_VOLUME}" >/dev/null 2>&1 || true
    "${DOCKER_BIN}" image rm "${SERVER_IMAGE}" "${OPERATOR_IMAGE}" "${AGENT_IMAGE}" >/dev/null 2>&1 || true
    rm -rf "${TEMP_DIR}"
  else
    echo "preserving cluster ${CLUSTER_NAME}, container ${SERVER_CONTAINER}, and ${TEMP_DIR}" >&2
  fi
  exit "${status}"
}
trap cleanup EXIT INT TERM

for command in "${KIND_BIN}" "${KUBECTL_BIN}" "${HELM_BIN}" "${DOCKER_BIN}" go curl; do require_command "${command}"; done
"${DOCKER_BIN}" info >/dev/null
"${KIND_BIN}" delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
"${DOCKER_BIN}" rm -f "${SERVER_CONTAINER}" >/dev/null 2>&1 || true

if [[ "${XISNOVE_KIND_E2E_PREBUILT:-0}" == "1" ]]; then
  echo "using accepted prebuilt Xisnove e2e images"
else
  echo "building digest-pinned Xisnove e2e images"
  "${DOCKER_BIN}" build -f "${ROOT_DIR}/integration/testdata/kind/Dockerfile.server" -t "${SERVER_IMAGE}" "${ROOT_DIR}"
  "${DOCKER_BIN}" build -f "${ROOT_DIR}/integration/testdata/kind/Dockerfile.operator" -t "${OPERATOR_IMAGE}" "${ROOT_DIR}"
  "${DOCKER_BIN}" build -f "${ROOT_DIR}/integration/testdata/kind/Dockerfile.agent" -t "${AGENT_IMAGE}" "${ROOT_DIR}"
fi

"${KIND_BIN}" create cluster --name "${CLUSTER_NAME}" --image "${NODE_IMAGE}" --config "${ROOT_DIR}/integration/testdata/kind/cluster.yaml" --kubeconfig "${KUBECONFIG_PATH}" --wait 120s
"${KIND_BIN}" load docker-image --name "${CLUSTER_NAME}" "${OPERATOR_IMAGE}" "${AGENT_IMAGE}"

printf '%s\n' 'kind-e2e-admin-password-with-sufficient-length' >"${TEMP_DIR}/admin-password"
printf '%s\n' 'kind-e2e-cursor-signing-key-32-bytes-minimum' >"${TEMP_DIR}/cursor-key"
chmod 0440 "${TEMP_DIR}/admin-password" "${TEMP_DIR}/cursor-key"
if SECRET_FILE_GROUP="$(stat -c '%g' "${TEMP_DIR}/admin-password" 2>/dev/null)"; then
  :
else
  SECRET_FILE_GROUP="$(stat -f '%g' "${TEMP_DIR}/admin-password")"
fi
"${DOCKER_BIN}" volume create "${DATA_VOLUME}" >/dev/null
"${DOCKER_BIN}" run --rm -v "${DATA_VOLUME}:/data" "${SERVER_IMAGE}" db migrate --database-profile sqlite --database-url /data/xisnove.db
"${DOCKER_BIN}" run --rm --group-add "${SECRET_FILE_GROUP}" -v "${DATA_VOLUME}:/data" -v "${TEMP_DIR}/admin-password:/run/admin-password:ro" "${SERVER_IMAGE}" admin bootstrap --database-profile sqlite --database-url /data/xisnove.db --email kind-e2e@example.test --password-file /run/admin-password
"${DOCKER_BIN}" run -d --name "${SERVER_CONTAINER}" --network kind --group-add "${SECRET_FILE_GROUP}" -p 127.0.0.1::8080 -v "${DATA_VOLUME}:/data" -v "${TEMP_DIR}/cursor-key:/run/cursor-key:ro" "${SERVER_IMAGE}" serve --listen 0.0.0.0:8080 --database-profile sqlite --database-url /data/xisnove.db --cursor-signing-key-file /run/cursor-key >/dev/null

SERVER_PORT="$("${DOCKER_BIN}" port "${SERVER_CONTAINER}" 8080/tcp | awk -F: 'NR==1 {print $NF}')"
HOST_URL="http://127.0.0.1:${SERVER_PORT}"
for _ in $(seq 1 90); do
  if curl --fail --silent --show-error "${HOST_URL}/readyz" >/dev/null; then break; fi
  sleep 1
done
curl --fail --silent --show-error "${HOST_URL}/readyz" >/dev/null

export XISNOVE_KIND_E2E=1
export XISNOVE_KIND_E2E_BASE_URL="${HOST_URL}"
export XISNOVE_KIND_E2E_CLUSTER_URL="http://${SERVER_CONTAINER}:8080"
export XISNOVE_KIND_E2E_SERVER_CONTAINER="${SERVER_CONTAINER}"
export XISNOVE_KIND_E2E_CLUSTER_NAME="${CLUSTER_NAME}"
export XISNOVE_KIND_E2E_NAMESPACE="${NAMESPACE}"
export XISNOVE_KIND_E2E_OPERATOR_IMAGE="${OPERATOR_IMAGE}"
export XISNOVE_KIND_E2E_AGENT_IMAGE="${AGENT_IMAGE}"
export XISNOVE_KIND_E2E_UI_IMAGE="${UI_IMAGE}"
export XISNOVE_KIND_E2E_ADMIN_EMAIL="kind-e2e@example.test"
export XISNOVE_KIND_E2E_ADMIN_PASSWORD_FILE="${TEMP_DIR}/admin-password"
export XISNOVE_KIND_E2E_HELM_BIN="${HELM_BIN}"
export XISNOVE_KIND_E2E_KUBECTL_BIN="${KUBECTL_BIN}"
export XISNOVE_KIND_E2E_DOCKER_BIN="${DOCKER_BIN}"

cd "${ROOT_DIR}"
go test -timeout 20m -count=1 -v ./integration -run '^TestKubernetesEdgeKind$'
