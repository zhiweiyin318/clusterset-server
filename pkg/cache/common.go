package cache

import (
	"fmt"
	"k8s.io/klog/v2"
	"strings"
	"sync"

	"github.com/open-cluster-management/clusterset-server/pkg/cache/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
	rbacv1informers "k8s.io/client-go/informers/rbac/v1"
	rbacv1listers "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/tools/cache"
)

// subjectRecord is a cache record for the set of resources a subject can access
type subjectRecord struct {
	subject string
	names   sets.String
}

// reviewRequest is the resource we want to review
type reviewRequest struct {
	name string
	// the resource version of the namespace that was observed to make this request
	resourceVersion string
	// the map of role uid to resource version that was observed to make this request
	roleUIDToResourceVersion map[types.UID]string
	// the map of role binding uid to resource version that was observed to make this request
	roleBindingUIDToResourceVersion map[types.UID]string
}

// reviewRecord is a cache record for the result of a resource access review
type reviewRecord struct {
	*reviewRequest
	users  []string
	groups []string
}

// reviewRecordKeyFn is a key func for reviewRecord objects
func reviewRecordKeyFn(obj interface{}) (string, error) {
	reviewRecord, ok := obj.(*reviewRecord)
	if !ok {
		return "", fmt.Errorf("expected reviewRecord")
	}
	return reviewRecord.name, nil
}

// subjectRecordKeyFn is a key func for subjectRecord objects
func subjectRecordKeyFn(obj interface{}) (string, error) {
	subjectRecord, ok := obj.(*subjectRecord)
	if !ok {
		return "", fmt.Errorf("expected subjectRecord")
	}
	return subjectRecord.subject, nil
}

// LastSyncResourceVersioner is any object that can divulge a LastSyncResourceVersion
type LastSyncResourceVersioner interface {
	LastSyncResourceVersion() string
}

type unionLastSyncResourceVersioner []LastSyncResourceVersioner

func (u unionLastSyncResourceVersioner) LastSyncResourceVersion() string {
	resourceVersions := []string{}
	for _, versioner := range u {
		resourceVersions = append(resourceVersions, versioner.LastSyncResourceVersion())
	}
	return strings.Join(resourceVersions, "")
}

type skipSynchronizer interface {
	// SkipSynchronize returns true if if its safe to skip synchronization of the cache based on provided token from previous observation
	SkipSynchronize(prevState string, versionedObjects ...LastSyncResourceVersioner) (skip bool, currentState string)
}

type statelessSkipSynchronizer struct{}

func (rs *statelessSkipSynchronizer) SkipSynchronize(prevState string, versionedObjects ...LastSyncResourceVersioner) (skip bool, currentState string) {
	resourceVersions := []string{}
	for i := range versionedObjects {
		resourceVersions = append(resourceVersions, versionedObjects[i].LastSyncResourceVersion())
	}
	currentState = strings.Join(resourceVersions, ",")
	skip = currentState == prevState
	klog.Infoln("rs SkipSynchronize...",prevState,currentState)
	return skip, currentState
}

type SyncedClusterRoleLister interface {
	rbacv1listers.ClusterRoleLister
	LastSyncResourceVersioner
}

type SyncedClusterRoleBindingLister interface {
	rbacv1listers.ClusterRoleBindingLister
	LastSyncResourceVersioner
}

type syncedClusterRoleLister struct {
	rbacv1listers.ClusterRoleLister
	versioner LastSyncResourceVersioner
}

func (l syncedClusterRoleLister) LastSyncResourceVersion() string {
	return l.versioner.LastSyncResourceVersion()
}

type syncedClusterRoleBindingLister struct {
	rbacv1listers.ClusterRoleBindingLister
	versioner LastSyncResourceVersioner
}

func (l syncedClusterRoleBindingLister) LastSyncResourceVersion() string {
	return l.versioner.LastSyncResourceVersion()
}

type AuthCache struct {
	// allKnownNames we track all the known resource names, so we can detect deletes.
	// TODO remove this in favor of a list/watch mechanism for projects
	allKnownNames sets.String

	clusterroleLister        SyncedClusterRoleLister
	clusterrolebindingLister SyncedClusterRoleBindingLister

	lastSyncResourceVersioner       LastSyncResourceVersioner
	policyLastSyncResourceVersioner LastSyncResourceVersioner
	skip                            skipSynchronizer
	lastState                       string

	clusterRoleBindingResourceVersions sets.String
	clusterRoleResourceVersions        sets.String

	reviewRecordStore       cache.Store
	userSubjectRecordStore  cache.Store
	groupSubjectRecordStore cache.Store

	syncRequests func() ([]*reviewRequest, error)

	reviewer rbac.Reviewer
	group    string
	resource string

	watchers    []CacheWatcher
	watcherLock sync.Mutex
}

func NewAutchCache(reviewer rbac.Reviewer,
	clusterroleInformer rbacv1informers.ClusterRoleInformer,
	clusterrolebindingInformer rbacv1informers.ClusterRoleBindingInformer,
	group, resource string,
	lastSyncResourceVersioner LastSyncResourceVersioner,
	syncRequestFunc func() ([]*reviewRequest, error),
) *AuthCache {
	scrLister := syncedClusterRoleLister{
		clusterroleInformer.Lister(),
		clusterroleInformer.Informer(),
	}
	scrbLister := syncedClusterRoleBindingLister{
		clusterrolebindingInformer.Lister(),
		clusterrolebindingInformer.Informer(),
	}
	result := &AuthCache{
		clusterroleLister:               scrLister,
		clusterrolebindingLister:        scrbLister,
		syncRequests:                    syncRequestFunc,
		lastSyncResourceVersioner:       lastSyncResourceVersioner,
		policyLastSyncResourceVersioner: unionLastSyncResourceVersioner{scrLister, scrbLister},

		reviewer: reviewer,
		group:    group,
		resource: resource,

		clusterRoleResourceVersions:        sets.NewString(),
		clusterRoleBindingResourceVersions: sets.NewString(),

		reviewRecordStore:       cache.NewStore(reviewRecordKeyFn),
		userSubjectRecordStore:  cache.NewStore(subjectRecordKeyFn),
		groupSubjectRecordStore: cache.NewStore(subjectRecordKeyFn),

		skip: &statelessSkipSynchronizer{},

		watchers: []CacheWatcher{},
	}

	return result
}

func (ac *AuthCache) syncResources(userSubjectRecordStore cache.Store, groupSubjectRecordStore cache.Store, reviewRecordStore cache.Store) sets.String {
	names := sets.NewString()
	requests, err := ac.syncRequests()
	if err != nil {
		utilruntime.HandleError(err)
		return nil
	}

	for _, request := range requests {
		names.Insert(request.name)
		if err := ac.syncRequest(request, userSubjectRecordStore, groupSubjectRecordStore, reviewRecordStore); err != nil {
			utilruntime.HandleError(fmt.Errorf("error synchronizing: %v", err))
		}
	}

	return names
}

// syncRequest takes a reviewRequest and determines if it should update the caches supplied, it is not thread-safe
func (ac *AuthCache) syncRequest(request *reviewRequest, userSubjectRecordStore cache.Store, groupSubjectRecordStore cache.Store, reviewRecordStore cache.Store) error {
	lastKnownValue, err := lastKnown(reviewRecordStore, request.name)
	if err != nil {
		return err
	}

	if skipReview(request, lastKnownValue) {
		return nil
	}

	name := request.name
	review, err := ac.reviewer.Review(ac.group, ac.resource, name)
	if err != nil {
		return err
	}

	usersToRemove := sets.NewString()
	groupsToRemove := sets.NewString()
	if lastKnownValue != nil {
		usersToRemove.Insert(lastKnownValue.users...)
		usersToRemove.Delete(review.Users()...)
		groupsToRemove.Insert(lastKnownValue.groups...)
		groupsToRemove.Delete(review.Groups()...)
	}

	deleteResourceFromSubjects(userSubjectRecordStore, usersToRemove.List(), name)
	deleteResourceFromSubjects(groupSubjectRecordStore, groupsToRemove.List(), name)
	addSubjectsToNamespace(userSubjectRecordStore, review.Users(), name)
	addSubjectsToNamespace(groupSubjectRecordStore, review.Groups(), name)
	cacheReviewRecord(request, lastKnownValue, review, reviewRecordStore)
	ac.notifyWatchers(name, lastKnownValue, sets.NewString(review.Users()...), sets.NewString(review.Groups()...))
	return nil
}

// synchronize runs a a full synchronization over the cache data.  it must be run in a single-writer model, it's not thread-safe by design.
func (ac *AuthCache) synchronize() {
	klog.Infoln("AuthCache synchronize....",ac.group," ",ac.resource)
	// if none of our internal reflectors changed, then we can skip reviewing the cache
	skip, currentState := ac.skip.SkipSynchronize(ac.lastState, ac.lastSyncResourceVersioner, ac.policyLastSyncResourceVersioner)
	if skip {
		klog.Infoln("AuthCache synchronize skip....",ac.group,ac.resource,ac.lastState, ac.lastSyncResourceVersioner.LastSyncResourceVersion(), ac.policyLastSyncResourceVersioner.LastSyncResourceVersion())
		// return
	}

	// by default, we update our current caches and do an incremental change
	userSubjectRecordStore := ac.userSubjectRecordStore
	groupSubjectRecordStore := ac.groupSubjectRecordStore
	reviewRecordStore := ac.reviewRecordStore

	// iterate over caches and synchronize our three caches
	newKnownNames := ac.syncResources(userSubjectRecordStore, groupSubjectRecordStore, reviewRecordStore)
	ac.synchronizeClusterRoleBindings(userSubjectRecordStore, groupSubjectRecordStore, reviewRecordStore)
	ac.purgeDeletedResources(ac.allKnownNames, newKnownNames, userSubjectRecordStore, groupSubjectRecordStore, reviewRecordStore)

	ac.allKnownNames = newKnownNames

	// we were able to update our cache since this last observation period
	ac.lastState = currentState
}

// synchronizeRoleBindings synchronizes access over each role binding
func (ac *AuthCache) synchronizeClusterRoleBindings(userSubjectRecordStore cache.Store, groupSubjectRecordStore cache.Store, reviewRecordStore cache.Store) {
	klog.Infoln("AuthCache synchronizeClusterRoleBindings....",ac.group," ",ac.resource)
	roleBindings, err := ac.clusterrolebindingLister.List(labels.Everything())
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	for _, roleBinding := range roleBindings {
		clusterRole, err := ac.clusterroleLister.Get(roleBinding.RoleRef.Name)
		if err != nil {
			continue
		}
		resources, all := getResoureNamesFromClusterRole(clusterRole, ac.group, ac.resource)
		if all {
			resources = ac.allKnownNames
		}
		klog.Infoln("AuthCache synchronizeClusterRoleBindings resource...",roleBinding.Name,resources.List())
		for _, name := range resources.List() {
			request := &reviewRequest{
				name:                            name,
				roleBindingUIDToResourceVersion: map[types.UID]string{roleBinding.UID: roleBinding.ResourceVersion},
				roleUIDToResourceVersion:        map[types.UID]string{clusterRole.UID: clusterRole.ResourceVersion},
			}
			klog.Infoln("AuthCache synchronizeClusterRoleBindings rolebinding....",name,roleBinding.UID,roleBinding.ResourceVersion)
			klog.Infoln("AuthCache synchronizeClusterRoleBindings role....",name,clusterRole.UID,clusterRole.ResourceVersion)
			if err := ac.syncRequest(request, userSubjectRecordStore, groupSubjectRecordStore, reviewRecordStore); err != nil {
				utilruntime.HandleError(fmt.Errorf("error synchronizing: %v", err))
			}
		}
	}
}

// purgeDeletedNamespaces will remove all namespaces enumerated in a reviewRecordStore that are not in the namespace set
func (ac *AuthCache) purgeDeletedResources(oldNames, newNames sets.String, userSubjectRecordStore cache.Store, groupSubjectRecordStore cache.Store, reviewRecordStore cache.Store) {
	reviewRecordItems := reviewRecordStore.List()
	for i := range reviewRecordItems {
		reviewRecord := reviewRecordItems[i].(*reviewRecord)
		if !newNames.Has(reviewRecord.name) {
			deleteResourceFromSubjects(userSubjectRecordStore, reviewRecord.users, reviewRecord.name)
			deleteResourceFromSubjects(groupSubjectRecordStore, reviewRecord.groups, reviewRecord.name)
			reviewRecordStore.Delete(reviewRecord)
		}
	}

	for name := range oldNames.Difference(newNames) {
		ac.notifyWatchers(name, nil, sets.String{}, sets.String{})
	}
}

func (ac *AuthCache) listNames(userInfo user.Info) sets.String {
	keys := sets.String{}
	user := userInfo.GetName()
	groups := userInfo.GetGroups()

	klog.Infoln("authCache listNames user:",user," group: ",groups)
	obj, exists, _ := ac.userSubjectRecordStore.GetByKey(user)
	if exists {
		subjectRecord := obj.(*subjectRecord)
		klog.Infoln("authCache user exists: ",subjectRecord)
		keys.Insert(subjectRecord.names.List()...)
	}

	for _, group := range groups {
		obj, exists, _ := ac.groupSubjectRecordStore.GetByKey(group)
		if exists {
			subjectRecord := obj.(*subjectRecord)
			klog.Infoln("authCache group exists: ",subjectRecord)
			keys.Insert(subjectRecord.names.List()...)
		}
	}

	return keys
}

func (ac *AuthCache) AddWatcher(watcher CacheWatcher) {
	ac.watcherLock.Lock()
	defer ac.watcherLock.Unlock()

	ac.watchers = append(ac.watchers, watcher)
}

func (ac *AuthCache) RemoveWatcher(watcher CacheWatcher) {
	ac.watcherLock.Lock()
	defer ac.watcherLock.Unlock()

	lastIndex := len(ac.watchers) - 1
	for i := 0; i < len(ac.watchers); i++ {
		if ac.watchers[i] == watcher {
			if i < lastIndex {
				// if we're not the last element, shift
				copy(ac.watchers[i:], ac.watchers[i+1:])
			}
			ac.watchers = ac.watchers[:lastIndex]
			break
		}
	}
}

func (ac *AuthCache) notifyWatchers(name string, exists *reviewRecord, users, groups sets.String) {
	ac.watcherLock.Lock()
	defer ac.watcherLock.Unlock()
	for _, watcher := range ac.watchers {
		watcher.GroupMembershipChanged(name, users, groups)
	}
}

// cacheReviewRecord updates the cache based on the request processed
func cacheReviewRecord(request *reviewRequest, lastKnownValue *reviewRecord, review rbac.Review, reviewRecordStore cache.Store) {
	reviewRecord := &reviewRecord{
		reviewRequest: &reviewRequest{name: request.name, roleUIDToResourceVersion: map[types.UID]string{}, roleBindingUIDToResourceVersion: map[types.UID]string{}},
		groups:        review.Groups(),
		users:         review.Users(),
	}
	// keep what we last believe we knew by default
	if lastKnownValue != nil {
		reviewRecord.resourceVersion = lastKnownValue.resourceVersion
		for k, v := range lastKnownValue.roleUIDToResourceVersion {
			reviewRecord.roleUIDToResourceVersion[k] = v
		}
		for k, v := range lastKnownValue.roleBindingUIDToResourceVersion {
			reviewRecord.roleBindingUIDToResourceVersion[k] = v
		}
	}

	// update the review record relative to what drove this request
	if len(request.resourceVersion) > 0 {
		reviewRecord.resourceVersion = request.resourceVersion
	}
	for k, v := range request.roleUIDToResourceVersion {
		reviewRecord.roleUIDToResourceVersion[k] = v
	}
	for k, v := range request.roleBindingUIDToResourceVersion {
		reviewRecord.roleBindingUIDToResourceVersion[k] = v
	}
	// update the cache record
	reviewRecordStore.Add(reviewRecord)
}

func lastKnown(reviewRecordStore cache.Store, namespace string) (*reviewRecord, error) {
	obj, exists, err := reviewRecordStore.GetByKey(namespace)
	if err != nil {
		return nil, err
	}
	if exists {
		return obj.(*reviewRecord), nil
	}
	return nil, nil
}

// skipReview returns true if the request was satisfied by the lastKnown
func skipReview(request *reviewRequest, lastKnownValue *reviewRecord) bool {

	// if your request is nil, you have no reason to make a review
	if request == nil {
		return true
	}

	// if you know nothing from a prior review, you better make a request
	if lastKnownValue == nil {
		return false
	}
	// if you are asking about a specific namespace, and you think you knew about a different one, you better check again
	if request.name != lastKnownValue.name {
		return false
	}

	// if you are making your request relative to a specific resource version, only make it if its different
	if len(request.resourceVersion) > 0 && request.resourceVersion != lastKnownValue.resourceVersion {
		return false
	}

	// if you see a new role binding, or a newer version, we need to do a review
	for k, v := range request.roleBindingUIDToResourceVersion {
		oldValue, exists := lastKnownValue.roleBindingUIDToResourceVersion[k]
		if !exists || v != oldValue {
			return false
		}
	}

	// if you see a new role, or a newer version, we need to do a review
	for k, v := range request.roleUIDToResourceVersion {
		oldValue, exists := lastKnownValue.roleUIDToResourceVersion[k]
		if !exists || v != oldValue {
			return false
		}
	}
	return true
}

// deleteResourceFromSubjects removes the resource from each subject
// if no other resources are active to that subject, it will also delete the subject from the cache entirely
func deleteResourceFromSubjects(subjectRecordStore cache.Store, subjects []string, name string) {
	for _, subject := range subjects {
		obj, exists, _ := subjectRecordStore.GetByKey(subject)
		if exists {
			subjectRecord := obj.(*subjectRecord)
			delete(subjectRecord.names, name)
			if len(subjectRecord.names) == 0 {
				subjectRecordStore.Delete(subjectRecord)
			}
		}
	}
}

// addSubjectsToResource adds the specified resource to each subject
func addSubjectsToNamespace(subjectRecordStore cache.Store, subjects []string, name string) {
	for _, subject := range subjects {
		var item *subjectRecord
		obj, exists, _ := subjectRecordStore.GetByKey(subject)
		if exists {
			item = obj.(*subjectRecord)
		} else {
			item = &subjectRecord{subject: subject, names: sets.NewString()}
			subjectRecordStore.Add(item)
		}
		item.names.Insert(name)
	}
}

func getResoureNamesFromClusterRole(clusterrole *rbacv1.ClusterRole, group, resource string) (sets.String, bool) {
	names := sets.NewString()
	all := false
	for _, rule := range clusterrole.Rules {
		if !rbac.APIGroupMatches(&rule, group) {
			continue
		}
		if !rbac.ResourceMatches(&rule, resource, "") {
			continue
		}
		if len(rule.ResourceNames) == 0 {
			all = true
		}
		names.Insert(rule.ResourceNames...)
	}
	return names, all
}
