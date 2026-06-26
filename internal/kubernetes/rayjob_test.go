package kubernetes

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
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

func TestDeleteStaleRayJobs(t *testing.T) {
	now := time.Date(2026, 6, 26, 1, 0, 0, 0, time.UTC)
	client := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{rayJobGVR: "RayJobList"},
		staleRayJob("delete-old", true, now.Add(-7*time.Hour)),
		staleRayJob("keep-fresh", true, now.Add(-5*time.Hour)),
		staleRayJob("keep-unmanaged", false, now.Add(-7*time.Hour)),
		staleRayJobNoCreation("keep-no-creation", true),
	)

	result, err := DeleteStaleRayJobs(context.Background(), client, "ray", now, 6*time.Hour)
	if err != nil {
		t.Fatalf("delete stale jobs: %v", err)
	}
	if result.Scanned != 3 || result.Deleted != 1 || result.SkippedFresh != 1 || result.SkippedNoCreationTime != 1 {
		t.Fatalf("cleanup result = %#v", result)
	}
	if !contains(result.DeletedJobIDs, "delete-old") {
		t.Fatalf("deleted job ids = %#v", result.DeletedJobIDs)
	}

	list, err := client.Resource(rayJobGVR).Namespace("ray").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	for _, item := range list.Items {
		if item.GetName() == "delete-old" {
			t.Fatalf("stale job still exists")
		}
	}
}

func TestDeleteStaleRayJobsIgnoresNotFound(t *testing.T) {
	now := time.Date(2026, 6, 26, 1, 0, 0, 0, time.UTC)
	client := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{rayJobGVR: "RayJobList"},
		staleRayJob("already-gone", true, now.Add(-7*time.Hour)),
	)
	client.PrependReactor("delete", "rayjobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(ktesting.DeleteAction)
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "ray.io", Resource: "rayjobs"}, deleteAction.GetName())
	})

	result, err := DeleteStaleRayJobs(context.Background(), client, "ray", now, 6*time.Hour)
	if err != nil {
		t.Fatalf("delete stale jobs: %v", err)
	}
	if result.Scanned != 1 || result.Deleted != 0 {
		t.Fatalf("cleanup result = %#v", result)
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

func staleRayJob(name string, managed bool, created time.Time) *unstructured.Unstructured {
	obj := staleRayJobNoCreation(name, managed)
	obj.SetCreationTimestamp(metav1.NewTime(created))
	return obj
}

func staleRayJobNoCreation(name string, managed bool) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("ray.io/v1")
	obj.SetKind("RayJob")
	obj.SetName(name)
	obj.SetNamespace("ray")
	if managed {
		obj.SetLabels(map[string]string{"ci.verl/job-id": name})
	}
	return obj
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
