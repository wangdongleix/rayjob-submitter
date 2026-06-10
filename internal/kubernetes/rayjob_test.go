package kubernetes

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func TestDeleteRayJobsByWorkflow(t *testing.T) {
	client := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{rayJobGVR: "RayJobList"},
		rayJob("keep", "1", "1"),
		rayJob("delete-a", "2", "1"),
		rayJob("delete-b", "2", "1"),
		rayJob("other-attempt", "2", "2"),
	)
	deleted, err := DeleteRayJobsByWorkflow(context.Background(), client, "ray", "2", "1")
	if err != nil {
		t.Fatalf("delete workflow jobs: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted = %#v", deleted)
	}
}

func rayJob(name, runID, attempt string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ray.io/v1",
			"kind":       "RayJob",
			"metadata": map[string]any{
				"name":      name,
				"namespace": "ray",
				"labels": map[string]any{
					"ci.github.com/run-id":      runID,
					"ci.github.com/run-attempt": attempt,
				},
			},
		},
	}
}
