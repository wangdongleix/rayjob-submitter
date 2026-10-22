package kubernetes

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

func OpenPodLogStream(ctx context.Context, client *kubernetes.Clientset, namespace, podName string, options corev1.PodLogOptions) (io.ReadCloser, error) {
	return client.CoreV1().Pods(namespace).GetLogs(podName, &options).Stream(ctx)
}
