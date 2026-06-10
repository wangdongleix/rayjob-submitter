package rayjob

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPatchRayJobTemplate(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	template, err := LoadRayJobTemplate("../../templates/verl-rayjob.yaml")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	originalName := template.GetName()
	entrypoint := BuildEntrypoint("tests/special_npu/run_ci_step.sh", []string{"--foo", "bar"})
	obj, err := PatchRayJobTemplate(template, RenderParams{
		JobID:          "vrj-123-1-123456-train-step",
		Namespace:      "ray",
		Repository:     "verl-project/verl",
		CommitSHA:      sha,
		Branch:         "main",
		GitHubRunID:    "123",
		GitHubAttempt:  "1",
		Image:          "quay.io/ascend/verl:rayjob-ci-" + sha,
		NameSuffix:     "train-step",
		Script:         "tests/special_npu/run_ci_step.sh",
		Args:           []string{"--foo", "bar"},
		Env:            map[string]string{"EXTRA_FLAG": "true"},
		WorkerReplicas: 2,
	})
	if err != nil {
		t.Fatalf("patch template: %v", err)
	}
	if template.GetName() != originalName {
		t.Fatalf("template object was mutated")
	}
	if obj.GetName() != "vrj-123-1-123456-train-step" || obj.GetNamespace() != "ray" {
		t.Fatalf("unexpected identity: %s/%s", obj.GetNamespace(), obj.GetName())
	}
	if got, _, _ := unstructured.NestedString(obj.Object, "spec", "entrypoint"); got != entrypoint {
		t.Fatalf("entrypoint = %q", got)
	}
	if got := obj.GetLabels()["ci.github.com/run-id"]; got != "123" {
		t.Fatalf("run-id label = %q", got)
	}
	if got := obj.GetAnnotations()["ci.verl/script"]; got != "tests/special_npu/run_ci_step.sh" {
		t.Fatalf("script annotation = %q", got)
	}
	image := "quay.io/ascend/verl:rayjob-ci-" + sha
	submitter := container(obj, []string{"spec", "submitterPodTemplate", "spec", "containers"}, "ray-job-submitter")
	if got := submitter["image"]; got != image {
		t.Fatalf("submitter image = %v", got)
	}
	head := container(obj, []string{"spec", "rayClusterSpec", "headGroupSpec", "template", "spec", "containers"}, "ray-head")
	if got := head["image"]; got != image {
		t.Fatalf("head image = %v", got)
	}
	workerGroups, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rayClusterSpec", "workerGroupSpecs")
	worker := workerGroups[0].(map[string]any)
	if worker["replicas"] != int64(2) || worker["minReplicas"] != int64(2) || worker["maxReplicas"] != int64(2) {
		t.Fatalf("worker replicas not patched: %#v", worker)
	}
	workerTemplate := worker["template"].(map[string]any)
	workerSpec := workerTemplate["spec"].(map[string]any)
	workerContainer := findContainer(workerSpec["containers"].([]any), "ray-worker")
	if workerContainer["image"] != image {
		t.Fatalf("worker image = %v", workerContainer["image"])
	}
	headLabels, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "rayClusterSpec", "headGroupSpec", "template", "metadata", "labels")
	if headLabels["ci.verl/job-id"] != "vrj-123-1-123456-train-step" || headLabels["ci.github.com/run-attempt"] != "1" {
		t.Fatalf("head labels = %#v", headLabels)
	}
	if !hasEnv(submitter["env"].([]any), "EXTRA_FLAG", "true") || !hasEnv(head["env"].([]any), "EXTRA_FLAG", "true") || !hasEnv(workerContainer["env"].([]any), "EXTRA_FLAG", "true") {
		t.Fatalf("extra env missing")
	}
}

func TestPatchRayJobTemplateRejectsEnvOverride(t *testing.T) {
	template, err := LoadRayJobTemplate("../../templates/verl-rayjob.yaml")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	_, err = PatchRayJobTemplate(template, RenderParams{
		JobID:          "vrj-123-1-123456-train-step",
		Namespace:      "ray",
		GitHubRunID:    "123",
		GitHubAttempt:  "1",
		Image:          "quay.io/ascend/verl:rayjob-ci-custom",
		NameSuffix:     "train-step",
		Script:         "tests/special_npu/run_ci_step.sh",
		Env:            map[string]string{"HF_ENDPOINT": "x"},
		WorkerReplicas: 2,
	})
	if err == nil {
		t.Fatalf("expected env override error")
	}
}

func container(obj *unstructured.Unstructured, path []string, name string) map[string]any {
	containers, _, _ := unstructured.NestedSlice(obj.Object, path...)
	return findContainer(containers, name)
}

func findContainer(containers []any, name string) map[string]any {
	for _, item := range containers {
		container := item.(map[string]any)
		if container["name"] == name {
			return container
		}
	}
	return nil
}

func hasEnv(env []any, name, value string) bool {
	for _, item := range env {
		m := item.(map[string]any)
		if m["name"] == name && m["value"] == value {
			return true
		}
	}
	return false
}
