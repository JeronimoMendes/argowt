// Package kube constructs Kubernetes clients using standard client-go loading
// rules. Construction only occurs after CLI help and version handling.
package kube

import (
	"fmt"

	argoclient "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Options struct {
	Kubeconfig string
	Context    string
}

type Connection struct {
	Context          string
	DefaultNamespace string
	RESTConfig       *rest.Config
	Argo             argoclient.Interface
	Kubernetes       kubernetes.Interface
}

type Builder interface {
	Build(Options) (*Connection, error)
}

type ClientBuilder struct{}

func (ClientBuilder) Build(options Options) (*Connection, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if options.Kubeconfig != "" {
		loadingRules.ExplicitPath = options.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if options.Context != "" {
		overrides.CurrentContext = options.Context
	}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	raw, err := loader.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	contextName := raw.CurrentContext
	if options.Context != "" {
		contextName = options.Context
	}
	if contextName == "" {
		return nil, fmt.Errorf("load kubeconfig: no current context")
	}
	if _, found := raw.Contexts[contextName]; !found {
		return nil, fmt.Errorf("load kubeconfig: context %q does not exist", contextName)
	}

	namespace, _, err := loader.Namespace()
	if err != nil {
		return nil, fmt.Errorf("resolve namespace: %w", err)
	}
	restConfig, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("construct Kubernetes client configuration: %w", err)
	}
	restConfig.UserAgent = "argowt"

	argo, err := argoclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("construct Argo Workflows client: %w", err)
	}
	core, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("construct Kubernetes client: %w", err)
	}
	return &Connection{
		Context:          contextName,
		DefaultNamespace: namespace,
		RESTConfig:       restConfig,
		Argo:             argo,
		Kubernetes:       core,
	}, nil
}
