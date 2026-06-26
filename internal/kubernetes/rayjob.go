package kubernetes

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var rayJobGVR = schema.GroupVersionResource{
	Group:    "ray.io",
	Version:  "v1",
	Resource: "rayjobs",
}

type StaleRayJobCleanupResult struct {
	Scanned                 int
	Deleted                 int
	SkippedFresh            int
	SkippedNoCreationTime   int
	DeletedJobIDs           []string
	SkippedFreshJobIDs      []string
	SkippedNoCreationJobIDs []string
}

func CreateRayJob(ctx context.Context, dc dynamic.Interface, namespace string, obj *unstructured.Unstructured) error {
	_, err := dc.Resource(rayJobGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

func GetRayJob(ctx context.Context, dc dynamic.Interface, namespace, name string) (*unstructured.Unstructured, error) {
	return dc.Resource(rayJobGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

func DeleteRayJobsByWorkflow(ctx context.Context, dc dynamic.Interface, namespace, runID, attempt string) ([]string, error) {
	selector := labels.Set{
		"ci.github.com/run-id":      runID,
		"ci.github.com/run-attempt": attempt,
	}.String()
	list, err := dc.Resource(rayJobGVR).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	deleted := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		name := item.GetName()
		err := dc.Resource(rayJobGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return deleted, err
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}

func DeleteStaleRayJobs(ctx context.Context, dc dynamic.Interface, namespace string, now time.Time, ttl time.Duration) (StaleRayJobCleanupResult, error) {
	list, err := dc.Resource(rayJobGVR).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: "ci.verl/job-id"})
	if err != nil {
		return StaleRayJobCleanupResult{}, err
	}
	result := StaleRayJobCleanupResult{
		DeletedJobIDs:           []string{},
		SkippedFreshJobIDs:      []string{},
		SkippedNoCreationJobIDs: []string{},
	}
	for _, item := range list.Items {
		result.Scanned++
		name := item.GetName()
		created := item.GetCreationTimestamp()
		if created.IsZero() {
			result.SkippedNoCreationTime++
			result.SkippedNoCreationJobIDs = append(result.SkippedNoCreationJobIDs, name)
			continue
		}
		if now.Sub(created.Time) < ttl {
			result.SkippedFresh++
			result.SkippedFreshJobIDs = append(result.SkippedFreshJobIDs, name)
			continue
		}
		err := dc.Resource(rayJobGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		result.Deleted++
		result.DeletedJobIDs = append(result.DeletedJobIDs, name)
	}
	return result, nil
}
