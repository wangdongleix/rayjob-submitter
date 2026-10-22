package kubernetes

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func FindRayJobSubmitterPod(ctx context.Context, client kubernetes.Interface, namespace, jobID string) (*corev1.Pod, error) {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var newest *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !strings.HasPrefix(pod.Name, jobID+"-") || !hasContainer(pod, "ray-job-submitter") {
			continue
		}
		// A completed predecessor can coexist with its replacement. Always
		// rediscover the newest submitter, including a pending replacement.
		if newest == nil || pod.CreationTimestamp.After(newest.CreationTimestamp.Time) ||
			(pod.CreationTimestamp.Equal(&newest.CreationTimestamp) && pod.Name > newest.Name) {
			newest = pod
		}
	}
	if newest != nil {
		if newest.Status.Phase == corev1.PodPending {
			return newest, fmt.Errorf("rayjob submitter pod %s is pending", newest.Name)
		}
		return newest, nil
	}
	return nil, fmt.Errorf("rayjob submitter pod not found")
}

func hasContainer(pod *corev1.Pod, name string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return true
		}
	}
	return false
}
