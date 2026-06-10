#!/usr/bin/env bash
set -euxo pipefail

CACHE_REPO=${CACHE_REPO:-/root/.cache/cicd_test/verl}
WORK_REPO=${WORK_REPO:-/verl}
EXPECTED_WORKERS=${EXPECTED_WORKERS:-2}

script_path=""
script_args=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --script-path)
            script_path="${2:?--script-path requires a value}"
            shift 2
            ;;
        *)
            script_args+=("$1")
            shift
            ;;
    esac
done

if [[ -z "${script_path}" ]]; then
    echo "--script-path is required" >&2
    exit 1
fi

if [[ ! -d "${CACHE_REPO}" ]]; then
    echo "${CACHE_REPO} does not exist" >&2
    exit 1
fi

copy_repo() {
    rm -rf "${WORK_REPO}"
    mkdir -p "${WORK_REPO}"
    cp -a "${CACHE_REPO}/." "${WORK_REPO}/"
}

install_repo() {
    if ! command -v uv >/dev/null 2>&1; then
        curl -LsSf https://astral.sh/uv/install.sh | sh
        # shellcheck disable=SC1090
        source "${HOME}/.local/bin/env"
    fi

    export PATH="${HOME}/.local/bin:${PATH}"

    cd "${WORK_REPO}"
    uv pip list --system
    uv pip install --system --no-deps -e "${WORK_REPO}"
    uv pip list --system
}

copy_repo
install_repo

python3 - <<'PY'
import os
import socket
import subprocess

import ray
from ray.util.scheduling_strategies import NodeAffinitySchedulingStrategy

CACHE_REPO = os.environ.get("CACHE_REPO", "/root/.cache/cicd_test/verl")
WORK_REPO = os.environ.get("WORK_REPO", "/verl")
EXPECTED_WORKERS = int(os.environ.get("EXPECTED_WORKERS", "2"))


def run_cmd(cmd: str) -> None:
    print(f"\n===== RUN: {cmd} =====", flush=True)
    subprocess.run(cmd, shell=True, executable="/bin/bash", check=True)


@ray.remote(num_cpus=1)
def setup_worker() -> str:
    hostname = socket.gethostname()
    print(f"===== Worker setup starts on {hostname} =====", flush=True)

    run_cmd('cat /usr/local/Ascend/ascend-toolkit/latest/"$(uname -i)"-linux/ascend_toolkit_install.info')
    run_cmd("npu-smi info")
    run_cmd(
        f"""
        rm -rf {WORK_REPO}
        mkdir -p {WORK_REPO}
        cp -a {CACHE_REPO}/. {WORK_REPO}/

        if ! command -v uv >/dev/null 2>&1; then
            curl -LsSf https://astral.sh/uv/install.sh | sh
            source "${{HOME}}/.local/bin/env"
        fi

        export PATH="${{HOME}}/.local/bin:${{PATH}}"

        cd {WORK_REPO}
        uv pip list --system
        uv pip install --system --no-deps -e {WORK_REPO}
        uv pip list --system
        python examples/data_preprocess/gsm8k.py --local_dataset_path "${{HOME}}/.cache/datasets/openai/gsm8k"
        ls -lh "${{HOME}}/data/gsm8k"
        """
    )

    print(f"===== Worker setup finished on {hostname} =====", flush=True)
    return hostname


ray.init(address="auto")

worker_nodes = []
for node in ray.nodes():
    if not node.get("Alive", False):
        continue
    if float(node.get("Resources", {}).get("CPU", 0)) > 0:
        worker_nodes.append(node)

print("===== Ray alive worker nodes =====", flush=True)
for node in worker_nodes:
    print(
        {
            "NodeID": node["NodeID"],
            "NodeManagerAddress": node.get("NodeManagerAddress"),
            "Resources": node.get("Resources"),
        },
        flush=True,
    )

if len(worker_nodes) != EXPECTED_WORKERS:
    raise RuntimeError(f"Expected {EXPECTED_WORKERS} workers, found {len(worker_nodes)}")

refs = [
    setup_worker.options(
        scheduling_strategy=NodeAffinitySchedulingStrategy(node_id=node["NodeID"], soft=False)
    ).remote()
    for node in worker_nodes
]

print(f"===== Worker setup completed on: {ray.get(refs)} =====", flush=True)
PY

cd "${WORK_REPO}"
bash "${script_path}" "${script_args[@]}"
rm -rf "${HOME}/ckpts"
