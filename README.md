# rayjob-submitter

Internal HTTP service for GitHub Actions to submit, watch, and clean up KubeRay `RayJob` resources.

The service is template-driven: it loads `templates/verl-rayjob.yaml`, deep-copies it per request, and only patches image, entrypoint, labels, env, and worker replicas.

## Configuration

| Env | Default |
| --- | --- |
| `LISTEN_ADDR` | `:8080` |
| `TARGET_NAMESPACE` | `ray` |
| `RAYJOB_TEMPLATE_PATH` | `templates/verl-rayjob.yaml` |
| `WORKER_REPLICAS` | `2` |
| `MAX_WORKER_REPLICAS` | `2` |
| `ALLOWED_REPOSITORY` | `verl-project/verl` |
| `ALLOWED_IMAGE_PREFIXES` | `quay.io/ascend/verl:,quay.io/albert54/verl-ci:` |
| `LOG_HEAD_WAIT_SECONDS` | `1800` |
| `WATCH_TIMEOUT_SECONDS` | `12600` |
| `STATUS_POLL_SECONDS` | `30` |
| `ALLOWED_SCRIPT_PREFIXES` | `tests/special_npu/,tests/special_e2e/,examples/data_preprocess/,/root/.cache/cicd_test/` |
| `MAX_ARGS` | `64` |
| `MAX_ARG_LENGTH` | `1024` |
| `MAX_EXTRA_ENV` | `64` |
| `MAX_ENV_VALUE_LENGTH` | `2048` |

## Deployment

```bash
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/deployment.yaml
```

Local access:

```bash
kubectl -n ci-system port-forward svc/rayjob-submitter 8080:80
curl -sS http://127.0.0.1:8080/healthz
```

## API

Submit:

```bash
curl -sS -X POST "${SUBMITTER_URL}/api/v1/rayjobs" \
  -H "Content-Type: application/json" \
  -d '{
    "repository": "verl-project/verl",
    "commit_sha": "0123456789abcdef0123456789abcdef01234567",
    "branch": "main",
    "github_run_id": "123456789",
    "github_run_attempt": "1",
    "image": "quay.io/albert54/verl-ci:ci-qwen3-06b-ppo-multinode",
    "name_suffix": "multinode-e2e",
    "script": "tests/special_npu/run_qwen3_06b_ppo_multinode.sh",
    "args": [],
    "env": {},
    "worker_replicas": 2
  }'
```

Watch:

```bash
curl -N -sS \
  "${SUBMITTER_URL}/api/v1/rayjobs/${JOB_ID}/watch?format=ndjson"
```

Cleanup:

```bash
curl -sS -X DELETE \
  "${SUBMITTER_URL}/api/v1/workflows/${GITHUB_RUN_ID}?attempt=${GITHUB_RUN_ATTEMPT}"
```

## Multinode Wrapper

`scripts/run_multinode_testing.sh` is the source for the preseeded RayJob wrapper used by the verl multinode workflow. Put it on the shared cache before running the workflow:

```bash
mkdir -p /root/.cache/cicd_test
cp scripts/run_multinode_testing.sh /root/.cache/cicd_test/run_multinode_testing.sh
chmod +x /root/.cache/cicd_test/run_multinode_testing.sh
```

`/root/.cache` must map to the shared `/nfs/verl_ci` storage used by the GitHub runner and all RayJob pods. The workflow copies the checked-out verl repository to `/root/.cache/cicd_test/verl`; the wrapper then copies that tree into `/verl` in the RayJob submitter pod and every Ray worker, installs the repository, prepares gsm8k on workers, and runs the script passed by `--script-path`.

Example submitter payload fields for the wrapper:

```json
{
  "script": "/root/.cache/cicd_test/run_multinode_testing.sh",
  "args": ["--script-path", "/verl/tests/special_npu/run_qwen3_06b_ppo_multinode.sh"],
  "env": {}
}
```

## GitHub Actions Helper

```bash
cat > /tmp/submit_and_wait_rayjob.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

name_suffix="$1"
script="$2"
args_json="${3:-[]}"
env_json="${4:-{}}"

: "${SUBMITTER_URL:?SUBMITTER_URL is required}"
: "${IMAGE:?IMAGE is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF_NAME:?GITHUB_REF_NAME is required}"
: "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"

github_run_attempt="${GITHUB_RUN_ATTEMPT:-1}"
worker_replicas="${WORKER_REPLICAS:-2}"

payload="$(jq -n \
  --arg repository "${GITHUB_REPOSITORY}" \
  --arg commit_sha "${GITHUB_SHA}" \
  --arg branch "${GITHUB_REF_NAME}" \
  --arg github_run_id "${GITHUB_RUN_ID}" \
  --arg github_run_attempt "${github_run_attempt}" \
  --arg image "${IMAGE}" \
  --arg name_suffix "${name_suffix}" \
  --arg script "${script}" \
  --argjson args "${args_json}" \
  --argjson env "${env_json}" \
  --argjson worker_replicas "${worker_replicas}" \
  '{repository:$repository,commit_sha:$commit_sha,branch:$branch,github_run_id:$github_run_id,github_run_attempt:$github_run_attempt,image:$image,name_suffix:$name_suffix,script:$script,args:$args,env:$env,worker_replicas:$worker_replicas}')"

response="$(curl -sS -X POST "${SUBMITTER_URL}/api/v1/rayjobs" \
  -H "Content-Type: application/json" \
  -d "${payload}")"
echo "${response}" | jq .

job_id="$(echo "${response}" | jq -r '.job_id')"
final_status_file="/tmp/rayjob-final-status-${job_id}"
rm -f "${final_status_file}"

set +e
curl -N -sS \
  "${SUBMITTER_URL}/api/v1/rayjobs/${job_id}/watch?format=ndjson" \
| while IFS= read -r event; do
  [[ -z "${event}" ]] && continue
  type="$(echo "${event}" | jq -r '.type // empty')"
  case "${type}" in
    log) echo "${event}" | jq -r '.line // ""' ;;
    status) echo "[rayjob status] $(echo "${event}" | jq -r '.status // "UNKNOWN"') $(echo "${event}" | jq -r '.message // ""')" ;;
    error) echo "[rayjob error] $(echo "${event}" | jq -r '.message // ""')" ;;
    final)
      status="$(echo "${event}" | jq -r '.status // "UNKNOWN"')"
      echo "[rayjob final] ${status} $(echo "${event}" | jq -r '.message // ""')"
      printf '%s' "${status}" > "${final_status_file}"
      ;;
    *) echo "${event}" ;;
  esac
done
set -e

final_status=""
[[ -f "${final_status_file}" ]] && final_status="$(cat "${final_status_file}")"
case "${final_status}" in
  SUCCEEDED) exit 0 ;;
  FAILED|TIMEOUT|NOT_FOUND) exit 1 ;;
  *) echo "Watch ended without final status."; exit 1 ;;
esac
EOF
chmod +x /tmp/submit_and_wait_rayjob.sh
```

## Development

```bash
go test ./...
go vet ./...
go build -o /tmp/rayjob-submitter-check ./cmd/rayjob-submitter
```
