#!/usr/bin/env bash
set -euxo pipefail

cd /verl

echo "===== Driver: ensure uv and install current repository ====="
if ! command -v uv >/dev/null 2>&1; then
    curl -LsSf https://astral.sh/uv/install.sh | sh
    # shellcheck disable=SC1090
    source "${HOME}/.local/bin/env"
fi

export PATH="${HOME}/.local/bin:${PATH}"

uv pip list --system
uv pip install --system --no-deps -e /verl
uv pip list --system

echo "===== Run environment check and data preprocess on every Ray worker ====="

python3 - <<'PY'
import os
import socket
import subprocess
import ray

from ray.util.scheduling_strategies import NodeAffinitySchedulingStrategy


EXPECTED_WORKERS = int(os.environ.get("EXPECTED_WORKERS", "2"))


def run_cmd(cmd: str, check: bool = True) -> None:
    print(f"\n===== RUN: {cmd} =====", flush=True)
    subprocess.run(
        cmd,
        shell=True,
        executable="/bin/bash",
        check=check,
    )


@ray.remote(num_cpus=1)
def setup_worker() -> str:
    hostname = socket.gethostname()
    print(f"===== Worker setup starts on {hostname} =====", flush=True)

    run_cmd(
        'cat /usr/local/Ascend/ascend-toolkit/latest/"$(uname -i)"-linux/ascend_toolkit_install.info',
        check=True,
    )
    run_cmd("npu-smi info", check=True)

    run_cmd(
        """
        if ! command -v uv >/dev/null 2>&1; then
            curl -LsSf https://astral.sh/uv/install.sh | sh
            source "${HOME}/.local/bin/env"
        fi

        export PATH="${HOME}/.local/bin:${PATH}"

        cd /verl

        echo "===== Initial pip list on worker ====="
        uv pip list --system

        echo "===== Install current repository on worker ====="
        uv pip install --system --no-deps -e /verl

        echo "===== Final pip list on worker ====="
        uv pip list --system
        """,
        check=True,
    )

    run_cmd(
        """
        cd /verl
        python examples/data_preprocess/gsm8k.py \
            --local_dataset_path "${HOME}/.cache/datasets/openai/gsm8k"
        """,
        check=True,
    )

    run_cmd("ls -lh ${HOME}/data/gsm8k || true", check=False)

    print(f"===== Worker setup finished on {hostname} =====", flush=True)
    return hostname


ray.init(address="auto")

nodes = ray.nodes()

worker_nodes = []
for node in nodes:
    if not node.get("Alive", False):
        continue

    resources = node.get("Resources", {})
    cpu = float(resources.get("CPU", 0))

    # head 配了 num-cpus=0，所以这里用 CPU > 0 排除 head。
    if cpu > 0:
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
    raise RuntimeError(
        f"Expected {EXPECTED_WORKERS} Ray worker nodes, "
        f"but found {len(worker_nodes)}. "
        f"Worker nodes: {[n.get('NodeManagerAddress') for n in worker_nodes]}"
    )

refs = []
for node in worker_nodes:
    node_id = node["NodeID"]
    ref = setup_worker.options(
        scheduling_strategy=NodeAffinitySchedulingStrategy(
            node_id=node_id,
            soft=False,
        )
    ).remote()
    refs.append(ref)

finished_hosts = ray.get(refs)
print(f"===== Worker setup completed on: {finished_hosts} =====", flush=True)
PY

echo "===== Running gsm8k e2e training tests with PPO on ASCEND NPU, FSDP backend, 2 nodes x 4 NPUs ====="

MODEL_ID=${MODEL_ID:-Qwen/Qwen3-0.6B}
MODEL_PATH=${MODEL_PATH:-${HOME}/.cache/models/${MODEL_ID}}

python3 -m verl.trainer.main_ppo \
    algorithm.adv_estimator=gae \
    data.train_files="${HOME}/data/gsm8k/train.parquet" \
    data.val_files="${HOME}/data/gsm8k/test.parquet" \
    data.train_batch_size=16 \
    data.max_prompt_length=512 \
    data.max_response_length=128 \
    data.shuffle=False \
    actor_rollout_ref.model.path="${MODEL_PATH}" \
    actor_rollout_ref.model.use_remove_padding=True \
    actor_rollout_ref.model.enable_gradient_checkpointing=True \
    actor_rollout_ref.actor.optim.lr=1e-6 \
    actor_rollout_ref.actor.ppo_mini_batch_size=8 \
    actor_rollout_ref.actor.ppo_micro_batch_size_per_gpu=1 \
    actor_rollout_ref.actor.fsdp_config.param_offload=True \
    actor_rollout_ref.actor.fsdp_config.optimizer_offload=True \
    actor_rollout_ref.actor.use_kl_loss=False \
    actor_rollout_ref.actor.ulysses_sequence_parallel_size=2 \
    actor_rollout_ref.actor.use_dynamic_bsz=True \
    actor_rollout_ref.actor.use_torch_compile=False \
    actor_rollout_ref.rollout.log_prob_micro_batch_size_per_gpu=1 \
    actor_rollout_ref.rollout.tensor_model_parallel_size=1 \
    actor_rollout_ref.rollout.name=vllm \
    actor_rollout_ref.rollout.gpu_memory_utilization=0.8 \
    actor_rollout_ref.rollout.log_prob_use_dynamic_bsz=True \
    actor_rollout_ref.rollout.enable_chunked_prefill=True \
    actor_rollout_ref.rollout.enforce_eager=False \
    +actor_rollout_ref.rollout.engine_kwargs.vllm.compilation_config.cudagraph_mode="FULL_AND_PIECEWISE" \
    critic.optim.lr=1e-5 \
    critic.model.use_remove_padding=True \
    critic.model.path="${MODEL_PATH}" \
    critic.model.enable_gradient_checkpointing=True \
    critic.ppo_micro_batch_size_per_gpu=1 \
    critic.ulysses_sequence_parallel_size=2 \
    critic.fsdp.param_offload=True \
    critic.fsdp.optimizer_offload=True \
    critic.use_dynamic_bsz=True \
    trainer.critic_warmup=0 \
    trainer.logger='["console"]' \
    trainer.project_name='verl_ppo_example_gsm8k_qwen3' \
    trainer.experiment_name='qwen3_06b_fsdp_multinode_2x4' \
    trainer.n_gpus_per_node=4 \
    trainer.nnodes=2 \
    trainer.save_freq=-1 \
    trainer.test_freq=-1 \
    trainer.total_epochs=1 \
    trainer.total_training_steps=1 \
    "$@"

echo "===== Cleanup ckpts ====="
rm -rf "${HOME}/ckpts"