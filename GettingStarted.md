# OCI Native Ingress Controller
Native Ingress controller helps customers by configuring a loadbalancer to act as a controller. 
It reads configurations from ingress resources and updates the loadbalancer to cater to those configurations. 
The native ingress controller itself is lightweight process and pushes all the request processing work to LB.


## Table of Contents
- [Usage Steps](#usage-steps)
  * [Prerequisites](#prerequisites)
  * [Principal Credential Setup](#principal-credential-setup)
    + [Instance Principal](#instance-principal)
    + [User Principal](#user-principal)
    + [Workload Identity](#workload-identity)
    + [Access Policies](#access-policies)
  * [Cert Manager](#cert-manager)
  * [Deployment](#deployment)
    + [Local Build and deploy](#local-build-and-deploy)
      - [Testing](#testing)
    + [Helm Deployment](#helm-deployment)
    + [Yaml Deployment](#yaml-deployment)
  * [Verification](#verification)
  * [Getting Started with Ingress](#getting-started-with-ingress)
  * [Feature Support](#feature-support)
    + [Routing](#routing)
      - [Host Based Routing](#host-based-routing)
      - [Path Based Routing](#path-based-routing)
    + [Default Backend Support](#default-backend-support)
    + [Ingress Class Support](#ingress-class-support)
    + [Pod Readiness Gate](#pod-readiness-gate)
      - [Configuration](#configuration)
      - [Checking the pod readiness condition](#checking-the-pod-readiness-condition)
    + [HTTPS/TLS Support](#httpstls-support)
      - [Sample configuration : Using Secret](#sample-configuration--using-secret)
      - [Sample configuration : Using Certificate](#sample-configuration--using-certificate)
    + [Custom Health Checker](#custom-health-checker)
    + [Web Firewall Integration](#web-firewall-integration)
  * [Dependency management](#dependency-management)
    + [How to introduce new modules or upgrade existing ones?](#how-to-introduce-new-modules-or-upgrade-existing-ones)
  * [Known Issues](#known-issues)
  * [FAQ](#faq)


## Usage Steps
This section describes steps to deploy and test OCI-Native-Ingress-Controller.

### Prerequisites
Kubernetes Cluster with Native Pod Networking setup.
Currently supported kubernetes versions are:
- 1.26
- 1.27
- 1.28
  
We set up the cluster with native pod networking and update the security rules. 
The documentation for NPN : [Doc Ref](https://docs.oracle.com/en-us/iaas/Content/ContEng/Concepts/contengpodnetworking_topic-OCI_CNI_plugin.htm).

Policy documentation for setting up security rules for load balancer:
 [Doc Ref](https://docs.oracle.com/en-us/iaas/Content/ContEng/Concepts/contengnetworkconfig.htm#securitylistconfig__security_rules_for_load_balancers)


### Principal Credential Setup
For native ingress controller to access other dependent services and perform operations, we need to configure it with a principal credential.
We can grant permissions to this principal which will be inherited by native ingress controller.

Different types of principal that are supported:
* [Instance Principal](#instance-principal)
* [User Principal](#user-principal)
* [Workload Identity](#workload-identity)

#### Instance Principal
This is the default authentication type. It uses the instance identity where the controller is deployed on (worker node).
To use this please update the authType to instance.
```
authType: instance
```

#### User Principal
Prepare user principal configuration to access OCI API as per sample template provided in [deploy/example/user-auth-config-example.yaml](/deploy/example/user-auth-config-example.yaml)

The documentation to How to use OCI CLI can be found below:
** [Required Keys and OCIDs](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/apisigningkey.htm)
** [SDK Configuration File](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/sdkconfig.htm).

Create a secret in your cluster and in the same namespace of your workload/application
```shell
kubectl create secret generic oci-config \
         --from-file=config=user-auth-config-example.yaml \
         --from-file=private-key=./oci/oci_api_key.pem \
         --namespace native-ingress-controller-system
```
If the deployment is done via helm update values.yaml as mentioned below.
```
authType: user
authSecretName: oci-config
```
If the deployment is done via manifest templates update deployment container args as mentioned below.
```
  args:
  - --lease-lock-name=oci-native-ingress-controller
  - --lease-lock-namespace=native-ingress-controller-system
  - --authType=user
  - --auth-secret-name=oci-config
  - --controller-class=oci.oraclecloud.com/native-ingress-controller
  - --compartment-id=
  - --subnet-id=
  - --v=4
```

#### Workload Identity
For workload identity, we have to use [Enhanced Clusters](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contengcreatingenhancedclusters.htm), and follow the public documentation to setup policies - [Doc](https://docs.oracle.com/en-us/iaas/Content/ContEng/Tasks/contenggrantingworkloadaccesstoresources.htm)

We have added the support to enable this via the authType flag as follows:
```
authType: workloadIdentity
```
Also, internally we would need to update the resource principal version and region according to your deployment resource.
These can be passed as env variables under [deployment.yaml](helm/oci-native-ingress-controller/templates/deployment.yaml)
```
  env:
    - name: OCI_RESOURCE_PRINCIPAL_VERSION
      value: "2.2"
    - name: OCI_RESOURCE_PRINCIPAL_REGION
      value: "us-ashburn-1"
```

#### Access Policies
Access to the resource should be explicitly granted using Policies for engaging ingress controller:
```
Allow <subject> to manage load-balancers in compartment <compartment-id>    
Allow <subject> to use virtual-network-family in compartment <compartment-id>
Allow <subject> to manage cabundles in compartment <compartment-id>
Allow <subject> to manage cabundle-associations in compartment <compartment-id>
Allow <subject> to manage leaf-certificates in compartment <compartment-id>
Allow <subject> to read leaf-certificate-bundles in compartment <compartment-id>
Allow <subject> to manage certificate-associations in compartment <compartment-id>
Allow <subject> to read certificate-authorities in compartment <compartment-id>
Allow <subject> to manage certificate-authority-associations in compartment <compartment-id>
Allow <subject> to read certificate-authority-bundles in compartment <compartment-id>
ALLOW <subject> to read public-ips in tenancy
ALLOW <subject> to manage floating-ips in tenancy 
Allow <subject> to manage waf-family in compartment <compartment-id>
Allow <subject> to read cluster-family in compartment <compartment-id>

Policy scope can be broadened to Tenancy or restricted to a particular location as shown below:
allow <subject> to manage load-balancers in tenancy
allow <subject> to manage load-balancers in compartment
```

[Policy Ref](https://docs.oracle.com/en-us/iaas/Content/Identity/policyreference/lbpolicyreference.htm)

### Cert Manager
The controller uses webhooks to push pod readiness gates to POD. Webhooks require TLS certificates that are generated and managed by a certificate manager.
Install the certificate manager with the following command:
```
kubectl apply -f https://github.com/jetstack/cert-manager/releases/latest/download/cert-manager.yaml
```

### Deployment
Ingress Controller can be deployed in the following ways
* [Local build deployment](#local-build-and-deploy)
* [Helm based deployment](#helm-deployment)
* [Yaml Deployment](#yaml-deployment)


#### Local Build and deploy

Run `make build` to compile your app. This will build the executable file in `./dist` folder.

Run `make image` to build the container image.  It will calculate the image
tag based on the most recent git tag, and whether the repo is "dirty" since
that tag (see `make version`).

Run `make push` to push the container image to `REGISTRY`.

Run `make clean` to clean up.

Run `build-push` to single step run build, containerize and push the generated image.


How to build

```
REGISTRY=<docker registry to push to> VERSION=<version> make build-push
```

How to deploy

```
helm install oci-native-ingress-controller helm/oci-native-ingress-controller --set "image.repository=<registry image detail>" --set "image.tag=<version>"
```

How to upgrade

```
helm upgrade oci-native-ingress-controller helm/oci-native-ingress-controller --set "image.repository=<registry image detail>" --set "image.tag=<version>"
```

##### Testing

Run `make test` and `make lint` to run tests and linters, respectively.  Like
building, this will use docker to execute.

The golangci-lint tool looks for configuration in `.golangci.yaml`.  If that
file is not provided, it will use its own built-in defaults.


#### Helm Deployment

Install kubectl - https://kubernetes.io/docs/tasks/tools/
Install helm - https://helm.sh/docs/intro/install/

We can use helm charts to make the ingress controller deployment.
```shell
helm install oci-native-ingress-controller helm/oci-native-ingress-controller
```
To uninstall the helm deployment
```
helm uninstall oci-native-ingress-controller
```
Execution example:
```
inbs@inbs:~/Downloads $ helm install oci-native-ingress-controller helm/oci-native-ingress-controller
NAME: oci-native-ingress-controller
LAST DEPLOYED: Mon Apr 17 21:38:09 2023
NAMESPACE: default
STATUS: deployed
REVISION: 1
TEST SUITE: None
inbs@inbs:~/Downloads $ kubectl get pods -n native-ingress-controller-system -o wide -w
NAME                                             READY   STATUS    RESTARTS   AGE   IP           NODE          NOMINATED NODE   READINESS GATES
oci-native-ingress-controller-54795499fd-6xlhn   1/1     Running   0          11s   10.0.10.57   10.0.10.182   <none>           <none>
```

#### Yaml Deployment

If you want to generate these yamls, it can be done using this command:
```
helm template --include-crds oci-native-ingress-controller helm/oci-native-ingress-controller --output-dir deploy/manifests
```
Note: This manifest folder also includes the CRD for CustomResourceDefinition of IngressClassParameters.

To execute the deployment,

We can directly deploy all the required resource for ingress controller via a simple command using the generated manifest folder:

```
kubectl apply -f deploy/manifests/oci-native-ingress-controller/crds
kubectl apply -f deploy/manifests/oci-native-ingress-controller/templates   
```

To Delete the deployment:
```
kubectl delete -f deploy/manifests/oci-native-ingress-controller/templates  --ignore-not-found=true 
kubectl delete -f deploy/manifests/oci-native-ingress-controller/crds  --ignore-not-found=true 
```

### Verification
We can verify the pod of native ingress controller as follows:
```shell
kubectl get pods --namespace native-ingress-controller-system --selector='app.kubernetes.io/name in (oci-native-ingress-controller)' 
```

### Getting Started with Ingress
We can create the IngressClass using the sample IngressClass file which will help set up the IngressClassParameter and IngressClass. 
We need to update the subnetId and compartmentId(Only if overriding values from Values.yaml) before executing the yaml.
```
apiVersion: "ingress.oraclecloud.com/v1beta1"
kind: IngressClassParameters
metadata:
  name: ingressparms-cr-test
  namespace: test
spec:
  compartmentId: "ocid1.compartment.oc1..aaaaaaaauckenasusv5odnc4bqspi77hgnjeo6ydq33hidzadpkjxyzzz"
  subnetId: "ocid1.subnet.oc1.iad.aaaaaaaaxaq3szzikh7cb53arlkdgbi4wz4g73qpnuqhdhqckr2d5zyyzzxx"
  loadBalancerName: "native-ic-lb"
  isPrivate: false
  maxBandwidthMbps: 400
  minBandwidthMbps: 100
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
    namespace: test
    apiGroup: ingress.oraclecloud.com
    kind: ingressclassparameters
    name: ingressparms-cr-test
```
Also, we can create our first ingress with a single service backend using the file: SampleIngress
```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: testecho1
  labels:
    app: testecho1
spec:
  minReadySeconds: 5
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 75%
  replicas: 4
  selector:
    matchLabels:
      app: testecho1
  template:
    metadata:
      labels:
        app: testecho1
    spec:
      containers:
        - name: testecho1
          image: registry.k8s.io/echoserver:1.4
          ports:
            - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: testecho1
spec:
  selector:
    app: testecho1
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-readiness
spec:
  rules:
    - host: "foo.bar.com"
      http:
        paths:
          - pathType: Exact
            path: "/testecho1"
            backend:
              service:
                name: testecho1
                port:
                  number: 80
```

### Feature Support
There are multiple features supported for the ingress controllers described in the following sections.

#### Routing
As part of the Ingress object definition, customers can define the routes to the applications that are exposed as Kubernetes service. Customers can use different routing strategies.
Host based and path based routing.

##### Host Based Routing
This would be an optional parameter that defines which host the rule gets applied. When the host parameter is not defined, the rule is applied to all incoming HTTP traffic.
```
...
spec:
  rules:
  - host: "foo.bar.com"
    http:
      paths:
        - path: /app1
          backend:
            serviceName: foo
            servicePort: 80
```
Requests that match "foo.bar.com" goes to service "foo". We also support for hostname wildcards like "*.bar.com"

##### Path Based Routing
An ingress user should be able to configure route based on the path provided in the configuration. 
Ingress Controller should be able to redirect paths to the respective service.
Sample:
```
...
spec:
  rules:
    - http:
      paths:
        - path: /app1
          backend:
            serviceName: ServiceA
            servicePort: 80
        - path: /app2
          backend:
            serviceName: ServiceB
            servicePort: 443
```
Eg: Here, IC will direct paths coming to /app1 to ServiceA and /app2 to ServiceB.

In some cases, multiple paths within an Ingress will match a request. In those cases precedence will be given first to the longest matching path. If two paths are still equally matched, precedence will be given to paths with an exact path type over prefix path type.
[Reference](https://kubernetes.io/docs/concepts/services-networking/ingress/#path-types)

#### Default Backend Support
When a requested route matches no ingresses, then we send the traffic to a single default backend. This is more like a 404 handler.
If no .spec.rules (shown in above examples) are specified, .spec.defaultBackend must be specified.
```
spec:
  defaultBackend:
    service:
      name: host-es
      port:
        number: 8080
```

#### Ingress Class Support
An ingress class helps in mapping an ingress resource to a controller in the case where there are multiple ingress controller implementations running inside a Kubernetes cluster. It can also be used to have ingresses with different LB shapes.

```
spec:
  controller: oci.oraclecloud.com/native-ingress-controller
```
#### Pod Readiness Gate
Pod readiness Gate provides customers to implement complex custom readiness checks for Kubernetes Pods. The pod readiness gate is needed to achieve full zero downtime rolling deployments.

##### Configuration
Please add the label podreadiness.ingress.oraclecloud.com/pod-readiness-gate-inject which will enable us to apply the pod readiness feature for the required namespace.
```
  kubectl label ns <namespace> podreadiness.ingress.oraclecloud.com/pod-readiness-gate-inject=enabled
```
##### Checking the pod readiness condition
The status of the readiness gate can be verified with kubectl get pods -o wide -w
```
NAME                                             READY   STATUS    RESTARTS   AGE   IP            NODE          NOMINATED NODE   READINESS GATES
testecho-7cdcfff87f-b6xt4                        1/1     Running   0          35s   10.0.10.242   10.0.10.135   <none>           0/1
testecho-7cdcfff87f-b6xt4                        1/1     Running   0          72s   10.0.10.242   10.0.10.135   <none>           1/1
```

#### HTTPS/TLS Support
We will be able to configure ingress routes those are HTTPS enabled. Customers can use a Kubernetes secret (TLS) or a certificate from certificate service.

- The controller will appropriately configure both listener and backend sets with provided credentials.
- In the case of Kubernetes secret we create a certificate service certificate and a ca bundle to configure the listener and backend set appropriately.
- In the case of certificates we use the certificate Id and certificate trust authority Id to configure listener and backend set.
- Customer can use the same credentials in their pods to make this an end to end SSL support.

##### Sample configuration : Using Secret
We create OCI certificate service certificates and cabundles for each kubernetes secret. Hence the content of the secret (ca.crt, tls.crt, tlskey) should conform to the certificate service standards.
Ref Documents:
- [Validation of Certificate chain](https://docs.oracle.com/en-us/iaas/Content/certificates/invalidcertificatechain.htm)
- [Validation of CA Bundle](https://docs.oracle.com/en-us/iaas/Content/certificates/invalidcabundlepem.htm)

Create a tls-secret containing the details on the certificate:
Secret format:
```
apiVersion: v1
data:
  ca.crt : <base64-encoded-certificate-chain>
  tls.crt: <base64-encoded-server-certificate>
  tls.key: <base64-encoded-private-key>
kind: Secret
metadata:
  name: demo-tls-secret
type: kubernetes.io/tls
```
Ingress Format: 
```
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-tls-secret
spec:
  tls:
  - hosts:
      - foo.bar.com
    secretName: tls-secret
  rules:
  - host: "foo.bar.com"
    http:
      paths:
      - pathType: Prefix
        path: "/TLSPath"
        backend:
          service:
            name: tls-test
            port:
              number: 443
```

##### Sample configuration : Using Certificate
Certificate should have the common name of the host specified.
```
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ingress-tls
  annotations:
    oci-native-ingress.oraclecloud.com/certificate-ocid: ocid1.certificate.oc1.iad.amaaaaaah4gjgpyaxlby5qciob5wnwa7cnm4brvq2tfta3ls6ngch3s6gabc
spec:
  rules:
  - host: "*.bar.com"
    http:
      paths:
      - pathType: Prefix
        path: "/TLSPath"
        backend:
          service:
            name: tls-test
            port:
              number: 443
```
#### Custom Health Checker
Support for adding custom values for backend set health checker and policy through annotations. Any changes to these values will be reconciled accordingly by the controller. Below are supported annotations in ingress spec:
```
// LB Annotation
oci-native-ingress.oraclecloud.com/id
 
// Listener Annotation
oci-native-ingress.oraclecloud.com/protocol
 
// Backendset Annotations
oci-native-ingress.oraclecloud.com/policy
oci-native-ingress.oraclecloud.com/healthcheck-protocol
oci-native-ingress.oraclecloud.com/healthcheck-port
oci-native-ingress.oraclecloud.com/healthcheck-path
oci-native-ingress.oraclecloud.com/healthcheck-interval-milliseconds
oci-native-ingress.oraclecloud.com/healthcheck-timeout-milliseconds
oci-native-ingress.oraclecloud.com/healthcheck-retries
oci-native-ingress.oraclecloud.com/healthcheck-return-code
oci-native-ingress.oraclecloud.com/healthcheck-response-regex
oci-native-ingress.oraclecloud.com/healthcheck-force-plaintext
```
References:
- [Policy](https://docs.oracle.com/en-us/iaas/Content/Balance/Reference/lbpolicies.htm)
- [Health-checker](https://docs.oracle.com/en-us/iaas/api/#/en/loadbalancer/20170115/HealthChecker/)

#### Web Firewall Integration
We can create a Web Application Firewalls (WAF) policy either through Console or API to protect the applications from threats and filter out bad traffic.
Once the WAF policy is created we can associate the OCI Load Balancer. We can add any desired conditions and rules to the web policies. 

In order to enable WAF, copy the OCI WAF policy OCID from the OCI WAF console and add the OCI WAF web Policy annotation to the IngressClass.
```
apiVersion: extensions/v1beta1
kind: IngressClass
metadata:
 annotations: 
     oci-native-ingress.oraclecloud.com/waf-policy-ocid: ocid1.webappfirewallpolicy.oc1.phx.amaaaaaah4gjgpya3sigtz347pqyr4n3b7udo2zw4jskownbq
```

### Dependency management
Module [vendoring](https://go.dev/ref/mod#vendoring) is used to manage 3d-party modules in the project.
`vendor/` folder contains all 3d-party modules.
All changes to those modules should be reflected in the remote VCS repository.

#### How to introduce new modules or upgrade existing ones?
1. Once new modules was added or updated, the next command should be executed:
   ```shell
   go mod tidy
   go mod vendor
   ```
   This command will update sources for that module in `vendor/` folder.
2. Then commit those changes.

### Known Issues
1. The loadbalancer has a limitation of 16 backend sets per load balancer. We create a backend set for every unique service and port combination. So if a customer has more such services they need to have new load balancers.
2. Each service port is mapped to a load balancer listener. For SSL configuration customer can specify only one key pair per listener which would be used for SSL termination. All the backend sets that are mapped to the listener will use the same issuer(CA Bundle) as the issuer of listener certificate. Any conflicting declarations across ingress resources for same listener will throw a validation error which will be logged in controller logs.
3. Any conflicting declarations for same backend set health checker and routing policy across ingress resources will throw a validation error which will be logged in controller logs.
4. For supporting ssl through kubernetes secrets, we generate respective certificates and ca bundles in certificate service. If we delete ingress resource, currently we only delete the load balancer resources.
The certificates need to be cleared by the customer.

### FAQ 
