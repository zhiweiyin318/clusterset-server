# How to deploy

Run `make deploy-server`

## Create clusterrole/rolebinding for a user

Create the following clusterrole/clusterrolebinding

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-clusterset
rules:
- apiGroups: ["view.open-cluster-management.io"]
  resources: ["managedclusters"]
  verbs: ["list"]
- apiGroups: ["cluster.open-cluster-management.io"]
  resources: ["managedclusters"]
  verbs: ["get"]
  resourceNames: ["cluster1"]
```

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: test-clusterset
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: test-clusterset
subjects:
- kind: User
  apiGroup: rbac.authorization.k8s.io
  name: user1
```

Then use user1 to get managedcluster

```
kubectl get managedcluster.view.open-cluster-management.io --as=user1
```