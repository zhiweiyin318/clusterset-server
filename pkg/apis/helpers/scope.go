package helpers

import (
	"fmt"

	"github.com/open-cluster-management/clusterset-server/pkg/cache/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	kauthorizer "k8s.io/apiserver/pkg/authorization/authorizer"
	rbaclisters "k8s.io/client-go/listers/rbac/v1"

	scopemetadata "github.com/openshift/library-go/pkg/authorization/scopemetadata"
)

const (
	scopesAllNamespaces = "*"
	ScopesKey           = "scopes.authorization.openshift.io"

	legacyGroupName                 = ""
	coreGroupName                   = ""
	kubeAuthorizationGroupName      = "authorization.k8s.io"
	openshiftAuthorizationGroupName = "authorization.openshift.io"
	imageGroupName                  = "image.openshift.io"
	networkGroupName                = "network.openshift.io"
	oauthGroupName                  = "oauth.openshift.io"
	projectGroupName                = "project.openshift.io"
	userGroupName                   = "user.openshift.io"
)

// ScopesToVisibleNamespaces returns a list of namespaces that the provided scopes have "get" access to.
// This exists only to support efficiently list/watch of projects (ACLed namespaces)
func ScopesToVisibleNamespaces(scopes []string, clusterRoleGetter rbaclisters.ClusterRoleLister, ignoreUnhandledScopes bool) (sets.String, error) {
	if len(scopes) == 0 {
		return sets.NewString("*"), nil
	}

	visibleNamespaces := sets.String{}

	errors := []error{}
	for _, scope := range scopes {
		found := false

		for _, evaluator := range ScopeEvaluators {
			if evaluator.Handles(scope) {
				found = true
				allowedNamespaces, err := evaluator.ResolveGettableNamespaces(scope, clusterRoleGetter)
				if err != nil {
					errors = append(errors, err)
					continue
				}

				visibleNamespaces.Insert(allowedNamespaces...)
				break
			}
		}

		if !found && !ignoreUnhandledScopes {
			errors = append(errors, fmt.Errorf("no scope evaluator found for %q", scope))
		}
	}

	return visibleNamespaces, kutilerrors.NewAggregate(errors)
}

const (
	UserIndicator        = "user:"
	ClusterRoleIndicator = "role:"
)

// ScopeEvaluator takes a scope and returns the rules that express it
type ScopeEvaluator interface {
	// Handles returns true if this evaluator can evaluate this scope
	Handles(scope string) bool
	ResolveGettableNamespaces(scope string, clusterRoleGetter rbaclisters.ClusterRoleLister) ([]string, error)
}

// ScopeEvaluators map prefixes to a function that handles that prefix
var ScopeEvaluators = []ScopeEvaluator{
	userEvaluator{},
	clusterRoleEvaluator{},
}

// scopes are in the format
// <indicator><indicator choice>
// we have the following formats:
// user:<scope name>
// role:<clusterrole name>:<namespace to allow the cluster role, * means all>
// TODO
// cluster:<comma-delimited verbs>:<comma-delimited resources>
// namespace:<namespace name>:<comma-delimited verbs>:<comma-delimited resources>

const (
	UserInfo        = UserIndicator + "info"
	UserAccessCheck = UserIndicator + "check-access"

	// UserListScopedProjects gives explicit permission to see the projects that this token can see.
	UserListScopedProjects = UserIndicator + "list-scoped-projects"

	// UserListAllProjects gives explicit permission to see the projects a user can see.  This is often used to prime secondary ACL systems
	// unrelated to openshift and to display projects for selection in a secondary UI.
	UserListAllProjects = UserIndicator + "list-projects"

	// UserFull includes all permissions of the user
	UserFull = UserIndicator + "full"
)

// user:<scope name>
type userEvaluator struct {
	scopemetadata.UserEvaluator
}

func (userEvaluator) ResolveGettableNamespaces(scope string, _ rbaclisters.ClusterRoleLister) ([]string, error) {
	switch scope {
	case UserFull, UserListAllProjects:
		return []string{"*"}, nil
	default:
		return []string{}, nil
	}
}

// escalatingScopeResources are resources that are considered escalating for scope evaluation
var escalatingScopeResources = []schema.GroupResource{
	{Group: coreGroupName, Resource: "secrets"},
	{Group: imageGroupName, Resource: "imagestreams/secrets"},
	{Group: oauthGroupName, Resource: "oauthauthorizetokens"},
	{Group: oauthGroupName, Resource: "oauthaccesstokens"},
	{Group: openshiftAuthorizationGroupName, Resource: "roles"},
	{Group: openshiftAuthorizationGroupName, Resource: "rolebindings"},
	{Group: openshiftAuthorizationGroupName, Resource: "clusterroles"},
	{Group: openshiftAuthorizationGroupName, Resource: "clusterrolebindings"},
	// used in Service admission to create a service with external IP outside the allowed range
	{Group: networkGroupName, Resource: "service/externalips"},

	{Group: legacyGroupName, Resource: "imagestreams/secrets"},
	{Group: legacyGroupName, Resource: "oauthauthorizetokens"},
	{Group: legacyGroupName, Resource: "oauthaccesstokens"},
	{Group: legacyGroupName, Resource: "roles"},
	{Group: legacyGroupName, Resource: "rolebindings"},
	{Group: legacyGroupName, Resource: "clusterroles"},
	{Group: legacyGroupName, Resource: "clusterrolebindings"},
}

// role:<clusterrole name>:<namespace to allow the cluster role, * means all>
type clusterRoleEvaluator struct {
	scopemetadata.ClusterRoleEvaluator
}

var clusterRoleEvaluatorInstance = clusterRoleEvaluator{}

// resolveRules doesn't enforce namespace checks
func (e clusterRoleEvaluator) resolveRules(scope string, clusterRoleGetter rbaclisters.ClusterRoleLister) ([]rbacv1.PolicyRule, error) {
	roleName, _, escalating, err := scopemetadata.ClusterRoleEvaluatorParseScope(scope)
	if err != nil {
		return nil, err
	}

	role, err := clusterRoleGetter.Get(roleName)
	if err != nil {
		if kapierrors.IsNotFound(err) {
			return []rbacv1.PolicyRule{}, nil
		}
		return nil, err
	}

	rules := []rbacv1.PolicyRule{}
	for _, rule := range role.Rules {
		if escalating {
			rules = append(rules, rule)
			continue
		}

		// rules with unbounded access shouldn't be allowed in scopes.
		if has(rule.Verbs, rbacv1.VerbAll) ||
			has(rule.Resources, rbacv1.ResourceAll) ||
			has(rule.APIGroups, rbacv1.APIGroupAll) {
			continue
		}
		// rules that allow escalating resource access should be cleaned.
		safeRule := removeEscalatingResources(rule)
		rules = append(rules, safeRule)
	}

	return rules, nil
}

func (e clusterRoleEvaluator) ResolveGettableNamespaces(scope string, clusterRoleGetter rbaclisters.ClusterRoleLister) ([]string, error) {
	_, scopeNamespace, _, err := scopemetadata.ClusterRoleEvaluatorParseScope(scope)
	if err != nil {
		return nil, err
	}
	rules, err := e.resolveRules(scope, clusterRoleGetter)
	if err != nil {
		return nil, err
	}

	attributes := kauthorizer.AttributesRecord{
		APIGroup:        coreGroupName,
		Verb:            "get",
		Resource:        "namespaces",
		ResourceRequest: true,
	}

	if rbac.RulesAllow(attributes, rules...) {
		return []string{scopeNamespace}, nil
	}

	return []string{}, nil
}

func has(set []string, value string) bool {
	for _, element := range set {
		if value == element {
			return true
		}
	}
	return false
}

func remove(array []string, item string) []string {
	newar := array[:0]
	for _, element := range array {
		if element != item {
			newar = append(newar, element)
		}
	}
	return newar
}

// removeEscalatingResources inspects a PolicyRule and removes any references to escalating resources.
// It has coarse logic for now.  It is possible to rewrite one rule into many for the finest grain control
// but removing the entire matching resource regardless of verb or secondary group is cheaper, easier, and errs on the side removing
// too much, not too little
func removeEscalatingResources(in rbacv1.PolicyRule) rbacv1.PolicyRule {
	var ruleCopy *rbacv1.PolicyRule

	for _, resource := range escalatingScopeResources {
		if !(has(in.APIGroups, resource.Group) && has(in.Resources, resource.Resource)) {
			continue
		}

		if ruleCopy == nil {
			// we're using a cache of cache of an object that uses pointers to data.  I'm pretty sure we need to do a copy to avoid
			// muddying the cache
			ruleCopy = in.DeepCopy()
		}

		ruleCopy.Resources = remove(ruleCopy.Resources, resource.Resource)
	}

	if ruleCopy != nil {
		return *ruleCopy
	}

	return in
}
