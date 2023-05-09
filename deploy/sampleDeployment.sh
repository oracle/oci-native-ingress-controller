#
# /*
# ** OCI Native Ingress Controller
# **
# ** Copyright (c) 2023 Oracle America, Inc. and its affiliates.
# ** Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
#  */
#
kubectl apply -f - << EOF

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo1
  labels:
    app: echo1
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo1
  template:
    metadata:
      labels:
        app: echo1
    spec:
      containers:
      - name: echo1
        image: registry.k8s.io/echoserver:1.4
        ports:
        - containerPort: 8080

---

apiVersion: v1
kind: Service
metadata:
  name: echo1
spec:
  selector:
    app: echo1
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080

---

apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo2
  labels:
    app: echo2
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo2
  template:
    metadata:
      labels:
        app: echo2
    spec:
      containers:
      - name: echo2
        image: registry.k8s.io/echoserver:1.4
        ports:
        - containerPort: 8080

---

apiVersion: v1
kind: Service
metadata:
  name: echo2
spec:
  selector:
    app: echo2
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
---

apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: default-ingress-class
  annotations:
    ingressclass.kubernetes.io/is-default-class: "true"
spec:
  controller: oci.oraclecloud.com/native-ingress-controller
  parameters:
    scope: Namespace
    namespace: default
    apiGroup: ingress.oraclecloud.com
    kind: ingressclassparameters
    name: ingressparms-cr-test

---

apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-np
spec:
  rules:
  - host: "foo.bar.com"
    http:
      paths:
      - pathType: Prefix
        path: "/echo1"
        backend:
          service:
            name: echo1
            port:
              number: 80

EOF