package kube

import (
	"context"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AccessReviewer asks the API server whether the current user may perform an
// action, through a SelfSubjectAccessReview.
type AccessReviewer struct {
	Client kubernetes.Interface
}

// Allowed checks whether the current user may perform the verb on the resource in the namespace.
// It returns API errors separately from permission denials.
func (reviewer AccessReviewer) Allowed(ctx context.Context, namespace, group, resource, verb string) (bool, error) {
	review, err := reviewer.Client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Group:     group,
				Resource:  resource,
				Verb:      verb,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}
	return review.Status.Allowed, nil
}
