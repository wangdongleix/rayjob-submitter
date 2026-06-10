package kubernetes

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

func StreamPodLogs(ctx context.Context, client *kubernetes.Clientset, namespace, podName, container string, w io.Writer) error {
	tail := int64(1000)
	stream, err := OpenPodLogStream(ctx, client, namespace, podName, container, tail)
	if err != nil {
		return err
	}
	defer stream.Close()
	_, err = io.Copy(w, stream)
	return err
}

func OpenPodLogStream(ctx context.Context, client *kubernetes.Clientset, namespace, podName, container string, tailLines int64) (io.ReadCloser, error) {
	return client.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
		TailLines: &tailLines,
	}).Stream(ctx)
}
