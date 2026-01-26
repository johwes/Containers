A demo golang program to show owner reference and relationships in a statefulset or deployment.
Requires rbac permissions for the SA running the pod in k8s.

In openshift i used,
```
oc adm policy add-role-to-user view -z default
```

Image is built and pushed here,
```
https://quay.io/repository/jwesterl/k8s-demo
```

