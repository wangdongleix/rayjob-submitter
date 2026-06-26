[PLANS]
- 2026-06-09T00:00Z [USER] Implement RayJob multinode E2E across two repos: keep wrapper source in rayjob-submitter, add workflow/job and multinode training script in /Users/albert/code/ascend/verl.

[DECISIONS]
- 2026-06-09T00:00Z [USER] Do not change rayjob-submitter service code; only add the preseed wrapper script and docs in this repo.
- 2026-06-09T00:00Z [USER] Superseded: allow submitter to accept the controlled absolute wrapper path /root/.cache/cicd_test/run_multinode_testing.sh.
- 2026-06-09T00:00Z [USER] Internal service no longer needs Bearer token auth, image prefix restriction, or script prefix restriction.
- 2026-06-09T00:00Z [USER] Superseded: restore image and script prefix checks, both supporting multiple comma-separated prefixes; script must support absolute and relative paths.
- 2026-06-10T00:00Z [USER] Do not restrict branch names beyond basic character validation; PR merge refs like 6672/merge must pass.
- 2026-06-25T12:49Z [USER] RayJob submitter should not force `cd /verl`; workflow/wrapper scripts own repository preparation and working-directory selection.
- 2026-06-26T01:42Z [USER] Add service-side stale RayJob cleanup for workflow attempts stopped from the GitHub UI before DELETE runs; default policy is all submitter-managed RayJobs older than 6h.
- 2026-06-26T03:33Z [USER] Support per-job safe template patching via whitelisted JSON Patch paths instead of a resource-specific override API.
- 2026-06-26T03:38Z [USER] Update `templates/verl-rayjob.yaml` and `templates/verl-rayjob-a3.yaml` for dual-machine 2x16-card CI training.

[PROGRESS]
- 2026-06-09T00:00Z [CODE] Added initial continuity file for the workspace because none existed.
- 2026-06-09T00:00Z [CODE] Added scripts/run_multinode_testing.sh wrapper source and README preseed instructions in rayjob-submitter.
- 2026-06-09T00:00Z [CODE] Created independent verl workflow /Users/albert/code/ascend/verl/.github/workflows/e2e_ascend_multinode.yml and training script /Users/albert/code/ascend/verl/tests/special_npu/run_qwen3_06b_ppo_multinode.sh.
- 2026-06-09T00:00Z [CODE] Updated the independent verl workflow helper to match the reference JSON defaulting style for args/env instead of compact parameter expansion.
- 2026-06-09T00:00Z [CODE] Relaxed submitter script validation to allow clean whitelisted absolute paths and added /root/.cache/cicd_test/ to defaults, deployment, README, and design docs.
- 2026-06-09T00:00Z [CODE] Removed token auth enforcement and removed image/script prefix config plus validation; cleaned deployment, docs, and the independent verl workflow token headers/env.
- 2026-06-09T00:00Z [CODE] Restored ALLOWED_IMAGE_PREFIXES and ALLOWED_SCRIPT_PREFIXES list configs; validation now accepts any configured prefix and script clean path may be absolute or relative.
- 2026-06-10T00:00Z [CODE] Removed main/v0.* branch allowlist from submit validation and documented the relaxed branch rule.
- 2026-06-25T12:49Z [CODE] Removed hard-coded `cd /verl` from BuildEntrypoint and updated tests/design docs to describe script-owned working-directory handling.
- 2026-06-26T01:42Z [CODE] Added configurable background stale RayJob cleanup loop, Kubernetes stale deletion helper, deployment env defaults, docs, and unit tests.
- 2026-06-26T03:33Z [CODE] Added `template_patches` request support with add/replace-only JSON Pointer validation for whitelisted resources, dshm sizeLimit, rayStartParams, and env append paths.
- 2026-06-26T03:38Z [CODE] Increased worker resource defaults in both requested templates to 2 replicas x 16 Ascend cards, 64 CPU, 256Gi memory, and 128Gi dshm per worker.

[DISCOVERIES]
- 2026-06-09T00:00Z [TOOL] Static checks passed: bash -n for both shell scripts and Ruby YAML parse for the new workflow.
- 2026-06-09T00:00Z [TOOL] go test ./..., go vet ./..., and go build ./cmd/rayjob-submitter passed after absolute script path support.
- 2026-06-09T00:00Z [TOOL] gofmt, go test ./..., go vet ./..., go build ./cmd/rayjob-submitter, and YAML parse for the verl workflow passed after removing token/prefix restrictions.
- 2026-06-09T00:00Z [TOOL] gofmt, go test ./..., go vet ./..., and go build ./cmd/rayjob-submitter passed after restoring multi-prefix validation.
- 2026-06-10T00:00Z [TOOL] gofmt, go test ./..., go vet ./..., and go build ./cmd/rayjob-submitter passed after branch validation relaxation.
- 2026-06-26T01:42Z [TOOL] gofmt, go test ./..., go vet ./..., go build ./cmd/rayjob-submitter, and Ruby YAML parse for deploy manifests passed after adding stale RayJob cleanup.
- 2026-06-26T03:33Z [TOOL] gofmt, go test ./..., go vet ./..., go build ./cmd/rayjob-submitter, and Ruby YAML parse for deploy manifests passed after adding safe template_patches.
- 2026-06-26T03:38Z [TOOL] Ruby YAML parse for templates/deploy, go test ./..., go vet ./..., and go build ./cmd/rayjob-submitter passed after 2x16-card template resource updates.

[OUTCOMES]
