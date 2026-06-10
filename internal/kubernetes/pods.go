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
	var pending *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !strings.HasPrefix(pod.Name, jobID+"-") || !hasContainer(pod, "ray-job-submitter") {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return pod, nil
		case corev1.PodSucceeded:
			return pod, nil
		case corev1.PodFailed:
			return pod, nil
		case corev1.PodPending:
			if pending == nil {
				pending = pod
			}
		}
	}
	if pending != nil {
		return pending, fmt.Errorf("rayjob submitter pod %s is pending", pending.Name)
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
