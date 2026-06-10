package kubernetes

import (
	"context"
	"fmt"
	"log"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type permission struct {
	Group     string
	Resource  string
	Verb      string
	Namespace string
}

func CheckPermissions(ctx context.Context, client kubernetes.Interface, namespace string) error {
	required := []permission{
		{Group: "ray.io", Resource: "rayjobs", Verb: "create", Namespace: namespace},
		{Group: "ray.io", Resource: "rayjobs", Verb: "get", Namespace: namespace},
		{Group: "ray.io", Resource: "rayjobs", Verb: "list", Namespace: namespace},
		{Group: "ray.io", Resource: "rayjobs", Verb: "delete", Namespace: namespace},
		{Resource: "pods", Verb: "list", Namespace: namespace},
		{Resource: "pods/log", Verb: "get", Namespace: namespace},
	}

	missing := []string{}
	for _, p := range required {
		review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group:     p.Group,
					Resource:  p.Resource,
					Verb:      p.Verb,
					Namespace: p.Namespace,
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		if !review.Status.Allowed {
			log.Printf("kubernetes permission missing: %s", p.String())
			missing = append(missing, p.String())
			continue
		}
		log.Printf("kubernetes permission allowed: %s", p.String())
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing kubernetes permissions: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (p permission) String() string {
	group := p.Group
	if group == "" {
		group = "core"
	}
	return fmt.Sprintf("%s %s/%s in %s", p.Verb, group, p.Resource, p.Namespace)
}
