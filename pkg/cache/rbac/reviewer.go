package rbac

import (
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

// Reviewer performs access reviews for a project by name
type Reviewer interface {
	Review(group, resource, name string) ([]rbacv1.Subject, error)
}

// reviewer performs access reviews for a project by name
type reviewer struct {
	subjectAccessEvaluator *SubjectAccessEvaluator
}

// NewReviewer knows how to make access control reviews for a resource by name
func NewReviewer(subjectAccessEvaluator *SubjectAccessEvaluator) Reviewer {
	return &reviewer{
		subjectAccessEvaluator: subjectAccessEvaluator,
	}
}

// Review performs a resource access review for the given resource by name
func (r *reviewer) Review(group, resource, name string) ([]rbacv1.Subject, error) {
	action := authorizer.AttributesRecord{
		Verb:            "get",
		APIGroup:        group,
		Resource:        resource,
		Name:            name,
		ResourceRequest: true,
	}
	return r.subjectAccessEvaluator.AllowedSubjects(action)
}
