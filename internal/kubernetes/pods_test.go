package kubernetes

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFindRayJobSubmitterPodSkipsHeadPod(t *testing.T) {
	jobID := "vrj-123-123456-manual-test"
	client := fake.NewSimpleClientset(
		pod(jobID+"-abc-head-sp2cz", "ray-head", corev1.PodRunning),
		pod(jobID+"-m8-worker-blzsc", "ray-worker", corev1.PodRunning),
		pod(jobID+"-5q8cb", "ray-job-submitter", corev1.PodRunning),
	)

	got, err := FindRayJobSubmitterPod(context.Background(), client, "ray", jobID)
	if err != nil {
		t.Fatalf("find submitter pod: %v", err)
	}
	if got.Name != jobID+"-5q8cb" {
		t.Fatalf("pod = %s", got.Name)
	}
}

func pod(name, container string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ray"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: container}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}
