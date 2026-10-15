package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NamespaceLister adapts the core client to the narrow namespace discovery
// interface consumed by the workflow repository.
type NamespaceLister struct {
	Client kubernetes.Interface
}

func (lister NamespaceLister) List(ctx context.Context, options metav1.ListOptions) ([]string, error) {
	list, err := lister.Client.CoreV1().Namespaces().List(ctx, options)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, namespace := range list.Items {
		names = append(names, namespace.Name)
	}
	return names, nil
}
