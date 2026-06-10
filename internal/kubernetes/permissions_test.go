package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestCheckPermissionsAllowed(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.Fake.PrependReactor("create", "selfsubjectaccessreviews", allowReview)

	if err := CheckPermissions(context.Background(), client, "ray"); err != nil {
		t.Fatalf("check permissions: %v", err)
	}
}

func TestCheckPermissionsMissing(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.Fake.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		review := action.(ktesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		allowed := review.Spec.ResourceAttributes.Resource != "pods/log"
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{Allowed: allowed},
		}, nil
	})

	err := CheckPermissions(context.Background(), client, "ray")
	if err == nil || !strings.Contains(err.Error(), "get core/pods/log in ray") {
		t.Fatalf("missing permission error = %v", err)
	}
}

func TestCheckPermissionsAPIError(t *testing.T) {
	client := fake.NewSimpleClientset()
	want := errors.New("api error")
	client.Fake.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, want
	})

	if err := CheckPermissions(context.Background(), client, "ray"); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func allowReview(action ktesting.Action) (bool, runtime.Object, error) {
	return true, &authorizationv1.SelfSubjectAccessReview{
		Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
	}, nil
}
