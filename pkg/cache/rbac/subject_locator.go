package rbac

import (
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	rbacv1listers "k8s.io/client-go/listers/rbac/v1"
)

type SubjectLocator interface {
	AllowedSubjects(attributes authorizer.Attributes) ([]rbacv1.Subject, error)
}

var _ = SubjectLocator(&SubjectAccessEvaluator{})

type SubjectAccessEvaluator struct {
	superUser string

	clusterRoleBindingLister rbacv1listers.ClusterRoleBindingLister
	clusterRoleLister        rbacv1listers.ClusterRoleLister
}

func NewSubjectAccessEvaluator(clusterRoles rbacv1listers.ClusterRoleLister, clusterRoleBindings rbacv1listers.ClusterRoleBindingLister, superUser string) *SubjectAccessEvaluator {
	subjectLocator := &SubjectAccessEvaluator{
		superUser:                superUser,
		clusterRoleBindingLister: clusterRoleBindings,
		clusterRoleLister:        clusterRoles,
	}
	return subjectLocator
}

func (r *SubjectAccessEvaluator) AllowedSubjects(requestAttributes authorizer.Attributes) ([]rbacv1.Subject, error) {
	subjects := []rbacv1.Subject{{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: user.SystemPrivilegedGroup}}
	if len(r.superUser) > 0 {
		subjects = append(subjects, rbacv1.Subject{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: r.superUser})
	}
	errorlist := []error{}

	clusterRoleBindings, err := r.clusterRoleBindingLister.List(labels.Everything())
	if err != nil {
		errorlist = append(errorlist, err)
	} else {
		for _, binding := range clusterRoleBindings {
			rules, err := r.getRulesFromRoleRef(binding.RoleRef)
			if err != nil {
				errorlist = append(errorlist, err)
				continue
			}
			if RulesAllow(requestAttributes, rules...) {
				subjects = append(subjects, binding.Subjects...)
			}
		}
	}

	return subjects, utilerrors.NewAggregate(errorlist)
}

func (r *SubjectAccessEvaluator) getRulesFromRoleRef(ref rbacv1.RoleRef) ([]rbacv1.PolicyRule, error) {
	clusterRole, err := r.clusterRoleLister.Get(ref.Name)
	if err != nil {
		return []rbacv1.PolicyRule{}, err
	}

	return clusterRole.Rules, nil
}
