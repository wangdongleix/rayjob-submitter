package kubernetes

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func NewClients() (*kubernetes.Clientset, dynamic.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, err
	}
	typedClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return typedClient, dynamicClient, nil
}
