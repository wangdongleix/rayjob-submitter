# verl RayJob Submitter 设计文档

## 目标

`rayjob-submitter` 是一个薄 HTTP 服务，让 GitHub Actions 不需要 Kubernetes 权限也能提交、观察和清理 KubeRay `RayJob`。

submitter 只负责：

- 校验请求
- 加载 `templates/verl-rayjob.yaml`
- 对模板 `DeepCopy()` 后 patch 必要字段
- 创建 RayJob
- 查询 RayJob 状态
- 输出 `/watch` 事件流
- 按 workflow run attempt 清理 RayJob
- 后台清理超过 TTL 的残留 RayJob

submitter 不理解 PPO/GRPO/MindSpeed 业务逻辑，不接收 raw shell，不接收任意 Kubernetes YAML，不执行 `kubectl`。

## 架构

```mermaid
flowchart TD
    gha[GitHub Actions] -->|POST /api/v1/rayjobs| submitter[rayjob-submitter]
    gha -->|GET /api/v1/rayjobs/job_id/watch| submitter
    gha -->|DELETE /api/v1/workflows/run_id?attempt=N| submitter

    template[templates/verl-rayjob.yaml] -->|startup load| submitter
    submitter -->|DeepCopy + patch| rayjob[RayJob]
    submitter -->|client-go| apiserver[Kubernetes API Server]
    apiserver --> operator[KubeRay Operator]
    operator --> cluster[RayCluster]
    cluster --> head[Ray Head Pod]
    cluster --> worker[Ray Worker Pods]
    cluster --> jobpod[RayJob submitter Pod]
    submitter -->|watch status| rayjob
    submitter -->|stream logs| jobpod
    submitter -->|periodic stale cleanup| rayjob
```

## 模板 Patch

模板是基础事实源，submitter 只 patch：

- `metadata.name` / `metadata.namespace`
- RayJob labels / annotations
- `spec.entrypoint`
- `submitterPodTemplate` 容器 image/env
- head / worker 容器 image/env
- head / worker Pod labels
- worker `replicas/minReplicas/maxReplicas`
- 请求中的安全 `template_patches` 白名单路径

保留模板里的 NPU hostPath、NFS cache、Ascend env、`PYTHONPATH`、nodeSelector、workingDir、imagePullPolicy。resources、`/dev/shm` sizeLimit、rayStartParams 和部分 env 可通过安全 `template_patches` 调整。

`entrypoint` 由 `script + args` 生成：

```bash
bash -lc 'set -euxo pipefail
bash/python <script> <args...>'
```

所有参数都 shell quote。submitter 不强制切换工作目录；仓库准备、`cd /verl` 等工作目录选择由 workflow 传入的 wrapper/script 自行处理。

## API

### `POST /api/v1/rayjobs`

请求：

```json
{
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
  "worker_replicas": 2,
  "template_patches": [
    {
      "op": "replace",
      "path": "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/resources/limits/memory",
      "value": "160Gi"
    }
  ]
}
```

响应：

```json
{
  "job_id": "vrj-123456789-1-143210-multinode-e2e",
  "rayjob_name": "vrj-123456789-1-143210-multinode-e2e",
  "namespace": "ray",
  "status": "SUBMITTED",
  "message": "RayJob submitted"
}
```

### `GET /api/v1/rayjobs/{job_id}`

返回 RayJob 状态，状态来自 `.status.jobStatus` 和 `.status.jobDeploymentStatus`。

### `GET /api/v1/rayjobs/{job_id}/watch?format=ndjson`

主观察接口。输出 RayJob 状态事件、日志事件和最终事件。

NDJSON 每行一个 JSON object：

```json
{"type":"status","status":"RUNNING","message":"RayJob is running"}
{"type":"log","pod":"xxx","container":"ray-job-submitter","line":"+ npu-smi info"}
{"type":"error","message":"log stream ended: context canceled"}
{"type":"final","status":"SUCCEEDED","message":"RayJob succeeded"}
{"type":"final","status":"FAILED","job_deployment_status":"ValidationFailed","message":"RayJob failed"}
```

`error` 事件不代表 RayJob 失败，最终成败只看 `final`。

`format=text` 输出：

```text
::status::<STATUS>::<MESSAGE>
::log::<LOG_LINE>
::error::<MESSAGE>
::final::<STATUS>::<MESSAGE>
```

### `DELETE /api/v1/workflows/{github_run_id}?attempt=<github_run_attempt>`

清理某次 workflow attempt 创建的所有 RayJob。RayJob CR 完成后不会自动消失，workflow 最后应调用该接口。

## 残留任务清理

workflow DELETE 是主清理路径。为防止 GitHub 网页端强制终止导致 cleanup step 未执行，服务默认启用后台 stale cleanup：

- 每 `STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS` 秒扫描一次。
- 只处理带 `ci.verl/job-id` 标签的 RayJob。
- 当 `metadata.creationTimestamp` 距当前时间超过 `STALE_RAYJOB_TTL_SECONDS` 时删除。
- 不区分 RayJob 当前状态，`RUNNING/PENDING/SUCCEEDED/FAILED/UNKNOWN` 均按 TTL 清理。
- 删除使用现有 `rayjobs list/delete` 权限，不需要额外 RBAC。

## Watch 行为

`/watch` 内部并发执行：

- status watcher：周期查询 RayJob CR，输出 status，遇到 `SUCCEEDED/FAILED/NOT_FOUND/TIMEOUT` 输出唯一 final。
- log streamer：等待 RayJob submitter Pod，读取 `ray-job-submitter` 容器日志，逐行输出 log。

日志流断开只输出 error，不直接判失败。RayJob status 才是最终裁决。

## 命名和标签

RayJob 名称：

```text
vrj-<github_run_id>-<github_run_attempt>-<HHMMSS>-<short_name_suffix>
```

KubeRay 要求 RayJob name 不超过 47 字符，因此 submitter 会截断 suffix 并保证名称符合 DNS label。

RayJob/head/worker Pod 都追加：

```text
ci.verl/job-id=<job_id>
ci.verl/name-suffix=<name_suffix>
ci.verl/run-id=<github_run_id>
ci.github.com/run-id=<github_run_id>
ci.github.com/run-attempt=<github_run_attempt>
```

## 参数校验

- `repository` 必须等于 `ALLOWED_REPOSITORY`
- `commit_sha` 必须匹配 `^[0-9a-f]{40}$`
- `branch` 必须非空且只包含字母、数字、点、下划线、斜杠、短横线
- `github_run_id` / `github_run_attempt` 必须为数字
- `image` 必须匹配 `ALLOWED_IMAGE_PREFIXES` 中的任一前缀
- `script` 必须是 `ALLOWED_SCRIPT_PREFIXES` 中任一前缀下的 clean path，支持相对路径和绝对路径
- `args` 数量和长度受限，禁止 shell 控制字符
- `env` key 必须匹配 `^[A-Z_][A-Z0-9_]{0,63}$`
- `env` 禁止覆盖 `PATH/HOME/PYTHONPATH/LD_LIBRARY_PATH/RAY_ADDRESS/RAY_HEAD_SERVICE_HOST`
- `worker_replicas` 必须大于 0 且不超过 `MAX_WORKER_REPLICAS`
- `template_patches` 仅支持 `add/replace`，路径必须命中安全白名单，quantity 字段必须是合法 Kubernetes quantity

## 配置

```text
LISTEN_ADDR=:8080
TARGET_NAMESPACE=ray
RAYJOB_TEMPLATE_PATH=templates/verl-rayjob.yaml
WORKER_REPLICAS=2
MAX_WORKER_REPLICAS=2
ALLOWED_REPOSITORY=verl-project/verl
ALLOWED_IMAGE_PREFIXES=quay.io/ascend/verl:,quay.io/albert54/verl-ci:
LOG_HEAD_WAIT_SECONDS=1800
WATCH_TIMEOUT_SECONDS=12600
STATUS_POLL_SECONDS=30
STALE_RAYJOB_CLEANUP_ENABLED=true
STALE_RAYJOB_TTL_SECONDS=21600
STALE_RAYJOB_CLEANUP_INTERVAL_SECONDS=600
ALLOWED_SCRIPT_PREFIXES=tests/special_npu/,tests/special_e2e/,examples/data_preprocess/,/root/.cache/cicd_test/
MAX_ARGS=64
MAX_ARG_LENGTH=1024
MAX_EXTRA_ENV=64
MAX_ENV_VALUE_LENGTH=2048
MAX_TEMPLATE_PATCHES=32
MAX_TEMPLATE_PATCH_VALUE_LENGTH=2048
```

## Template Patches

`template_patches` 是受限 JSON Patch，不是任意 RayJob YAML patch。第一版只允许资源和运行参数相关路径：

- submitter/head/worker 容器 `resources.requests/*` 和 `resources.limits/*`
- head/worker `dshm` volume 的 `emptyDir.sizeLimit`
- head/worker `rayStartParams/*`
- head/worker 容器 env 追加，路径必须以 `/env/-` 结尾

当前模板数组索引固定为：

- submitter container: `/spec/submitterPodTemplate/spec/containers/0`
- head container: `/spec/rayClusterSpec/headGroupSpec/template/spec/containers/0`
- head dshm volume: `/spec/rayClusterSpec/headGroupSpec/template/spec/volumes/1`
- worker group: `/spec/rayClusterSpec/workerGroupSpecs/0`
- worker container: `/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0`
- worker dshm volume: `/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/volumes/1`

## RBAC

ServiceAccount 需要：

- `rayjobs`: `create/get/list/delete`
- `pods`: `list`
- `pods/log`: `get`
- `selfsubjectaccessreviews`: `create`

禁止授予 `secrets`、`pods/exec`、`cluster-admin`、`nodes`、`persistentvolumes`。
