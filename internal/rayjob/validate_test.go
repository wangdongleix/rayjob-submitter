package rayjob

import (
	"strings"
	"testing"

	"github.com/verl-project/rayjob-submitter/internal/config"
)

func testConfig() config.Config {
	return config.Config{
		TargetNamespace:   "ray",
		WorkerReplicas:    2,
		MaxWorkerReplicas: 2,
		AllowedRepository: "verl-project/verl",
		AllowedImagePrefixes: []string{
			"quay.io/ascend/verl:",
			"quay.io/albert54/verl-ci:",
		},
		RayJobTemplatePath:  "../../templates/verl-rayjob.yaml",
		LogHeadWaitSeconds:  1800,
		WatchTimeoutSeconds: 12600,
		StatusPollSeconds:   30,
		AllowedScriptPrefixes: []string{
			"tests/special_npu/",
			"tests/special_e2e/",
			"examples/data_preprocess/",
			"/root/.cache/cicd_test/",
		},
		MaxArgs:                     64,
		MaxArgLength:                1024,
		MaxExtraEnv:                 64,
		MaxEnvValueLength:           2048,
		MaxTemplatePatches:          32,
		MaxTemplatePatchValueLength: 2048,
	}
}

func validRequest() SubmitRequest {
	sha := "0123456789abcdef0123456789abcdef01234567"
	return SubmitRequest{
		Repository:       "verl-project/verl",
		CommitSHA:        sha,
		Branch:           "main",
		GitHubRunID:      "123456789",
		GitHubRunAttempt: "1",
		Image:            "quay.io/ascend/verl:rayjob-ci-custom",
		NameSuffix:       "train-step",
		Script:           "tests/special_npu/run_ci_step.sh",
		Args:             []string{"--data", "/root/.cache/datasets/openai/gsm8k"},
		Env:              map[string]string{"EXTRA_FLAG": "true"},
		WorkerReplicas:   2,
	}
}

func TestValidateSubmitRequestValid(t *testing.T) {
	if err := ValidateSubmitRequest(validRequest(), testConfig()); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidateSubmitRequestAllowsWhitelistedAbsoluteScript(t *testing.T) {
	req := validRequest()
	req.Script = "/root/.cache/cicd_test/run_multinode_testing.sh"
	if err := ValidateSubmitRequest(req, testConfig()); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidateSubmitRequestAllowsMultipleImagePrefixes(t *testing.T) {
	req := validRequest()
	req.Image = "quay.io/albert54/verl-ci:custom"
	if err := ValidateSubmitRequest(req, testConfig()); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidateSubmitRequestAllowsPullRequestMergeBranch(t *testing.T) {
	req := validRequest()
	req.Branch = "6672/merge"
	if err := ValidateSubmitRequest(req, testConfig()); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidateSubmitRequestAllowsTemplatePatches(t *testing.T) {
	req := validRequest()
	req.TemplatePatches = []TemplatePatch{
		{
			Op:    "replace",
			Path:  "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/resources/limits/memory",
			Value: "160Gi",
		},
		{
			Op:    "replace",
			Path:  "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/volumes/1/emptyDir/sizeLimit",
			Value: "96Gi",
		},
		{
			Op:    "add",
			Path:  "/spec/rayClusterSpec/workerGroupSpecs/0/rayStartParams/memory-usage-threshold",
			Value: "0.98",
		},
		{
			Op:    "add",
			Path:  "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/env/-",
			Value: map[string]any{"name": "RAY_MEMORY_USAGE_THRESHOLD", "value": "0.98"},
		},
	}
	if err := ValidateSubmitRequest(req, testConfig()); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestValidateSubmitRequestRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name string
		edit func(*SubmitRequest)
	}{
		{"repository", func(r *SubmitRequest) { r.Repository = "fork/verl" }},
		{"commit sha", func(r *SubmitRequest) { r.CommitSHA = "bad" }},
		{"branch", func(r *SubmitRequest) { r.Branch = "bad branch" }},
		{"image", func(r *SubmitRequest) { r.Image = "" }},
		{"image prefix", func(r *SubmitRequest) { r.Image = "docker.io/verl:latest" }},
		{"worker replicas", func(r *SubmitRequest) { r.WorkerReplicas = 4 }},
		{"run id", func(r *SubmitRequest) { r.GitHubRunID = "abc" }},
		{"missing attempt", func(r *SubmitRequest) { r.GitHubRunAttempt = "" }},
		{"missing suffix", func(r *SubmitRequest) { r.NameSuffix = "" }},
		{"name suffix", func(r *SubmitRequest) { r.NameSuffix = "Bad_Name" }},
		{"script prefix", func(r *SubmitRequest) { r.Script = "scripts/run.sh" }},
		{"absolute script prefix", func(r *SubmitRequest) { r.Script = "/tmp/run.sh" }},
		{"script traversal", func(r *SubmitRequest) { r.Script = "tests/special_npu/../run.sh" }},
		{"script shell", func(r *SubmitRequest) { r.Script = "tests/special_npu/run.sh;rm" }},
		{"script extension", func(r *SubmitRequest) { r.Script = "tests/special_npu/run.txt" }},
		{"arg shell", func(r *SubmitRequest) { r.Args = []string{"a;b"} }},
		{"env key", func(r *SubmitRequest) { r.Env = map[string]string{"bad-key": "x"} }},
		{"protected env", func(r *SubmitRequest) { r.Env = map[string]string{"PYTHONPATH": "x"} }},
		{"template patch op", func(r *SubmitRequest) {
			r.TemplatePatches = []TemplatePatch{{Op: "remove", Path: "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/resources/limits/memory"}}
		}},
		{"template patch image", func(r *SubmitRequest) {
			r.TemplatePatches = []TemplatePatch{{Op: "replace", Path: "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/image", Value: "bad"}}
		}},
		{"template patch entrypoint", func(r *SubmitRequest) {
			r.TemplatePatches = []TemplatePatch{{Op: "replace", Path: "/spec/entrypoint", Value: "bad"}}
		}},
		{"template patch host path", func(r *SubmitRequest) {
			r.TemplatePatches = []TemplatePatch{{Op: "replace", Path: "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/volumes/0/hostPath/path", Value: "/tmp"}}
		}},
		{"template patch bad quantity", func(r *SubmitRequest) {
			r.TemplatePatches = []TemplatePatch{{Op: "replace", Path: "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/resources/limits/memory", Value: "a lot"}}
		}},
		{"template patch protected env", func(r *SubmitRequest) {
			r.TemplatePatches = []TemplatePatch{{Op: "add", Path: "/spec/rayClusterSpec/workerGroupSpecs/0/template/spec/containers/0/env/-", Value: map[string]any{"name": "PYTHONPATH", "value": "x"}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.edit(&req)
			if err := ValidateSubmitRequest(req, testConfig()); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestBuildEntrypoint(t *testing.T) {
	got := BuildEntrypoint("tests/special_npu/run.sh", []string{"--path", "/root/a b", "quote'value"})
	if !strings.HasPrefix(got, "bash -lc ") {
		t.Fatalf("entrypoint must use bash -lc: %q", got)
	}
	for _, want := range []string{
		"set -euxo pipefail",
		"tests/special_npu/run.sh",
		"--path",
		"/root/a b",
		"quote",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("entrypoint missing %q: %s", want, got)
		}
	}
	py := BuildEntrypoint("examples/data_preprocess/gsm8k.py", nil)
	if !strings.Contains(py, "python") || !strings.Contains(py, "examples/data_preprocess/gsm8k.py") {
		t.Fatalf("python entrypoint = %q", py)
	}
}

func TestJobIDIncludesNameSuffix(t *testing.T) {
	got := JobID("123456789", "1", 1780748778589, "multinode-e2e")
	if len(got) > MaxRayJobNameLength {
		t.Fatalf("JobID length = %d: %q", len(got), got)
	}
	if !strings.HasPrefix(got, "vrj-123456789-1-") || !strings.HasSuffix(got, "-multinode-e2e") {
		t.Fatalf("JobID = %q", got)
	}

	long := JobID("123456789012345678901234567890", "1000", 1780748778589, "very-long-multinode-e2e-suffix-name")
	if len(long) > MaxRayJobNameLength {
		t.Fatalf("long JobID length = %d: %q", len(long), long)
	}
}
