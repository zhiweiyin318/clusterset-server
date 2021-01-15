package rbac

import (
	"k8s.io/apiserver/pkg/authorization/authorizer"

	"github.com/openshift/library-go/pkg/authorization/authorizationutil"
)

// Review is a list of users and groups that can access a resource
type Review interface {
	Users() []string
	Groups() []string
	EvaluationError() string
}

type defaultReview struct {
	users           []string
	groups          []string
	evaluationError string
}

func (r *defaultReview) Users() []string {
	return r.users
}

// Groups returns the groups that can access a resource
func (r *defaultReview) Groups() []string {
	return r.groups
}

func (r *defaultReview) EvaluationError() string {
	return r.evaluationError
}

// Reviewer performs access reviews for a project by name
type Reviewer interface {
	Review(group, resource, name string) (Review, error)
}

// reviewer performs access reviews for a project by name
type reviewer struct {
	subjectAccessEvaluator SubjectLocator
}

// NewReviewer knows how to make access control reviews for a resource by name
func NewReviewer(subjectAccessEvaluator *SubjectAccessEvaluator) Reviewer {
	return &reviewer{
		subjectAccessEvaluator: subjectAccessEvaluator,
	}
}

// Review performs a resource access review for the given resource by name
func (r *reviewer) Review(group, resource, name string) (Review, error) {
	action := authorizer.AttributesRecord{
		Verb:            "get",
		APIGroup:        group,
		Resource:        resource,
		Name:            name,
		ResourceRequest: true,
	}

	subjects, err := r.subjectAccessEvaluator.AllowedSubjects(action)
	review := &defaultReview{}
	review.users, review.groups = authorizationutil.RBACSubjectsToUsersAndGroups(subjects, action.GetNamespace())
	if err != nil {
		review.evaluationError = err.Error()
	}
	return review, nil
}
