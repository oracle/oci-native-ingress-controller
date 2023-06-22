package auth

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/oracle/oci-native-ingress-controller/pkg/types"
	"github.com/oracle/oci-native-ingress-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
)

const (
	PrivateKey = "SSLPrivateData = `-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAyxPEO9rowYQ6/sjpD4VxnGdChokq4b8LyOcnIFRMueihl+8S\napqbe96A3etQaMBANx2FcuFt9FcPSJaJU93i9hkw/FPa5d2+Kr7wgE3pwOPXPqOI\nxuaeQfUIZ4QcGNSs1utsSbj/i3RvJDgrUOI+RypT4erpQX2cQZ5tplaDd2SxBYWW\nyZUkVIRPXKyJm4Yft1CsKDtbEzzIdh69DlfyfWRDYWxfD9D/RflmDafbunXo1OC2\nUJ3MHi+tD2NxgCFVvOWiiE+BMD28e3mGVg6WvoFtcutahnvrFocHDWnoMK269AbI\nrZ1WuUKBxOlbWLz9XbbsxFYDskRqNk22GtrQ3QIDAQABAoIBACU1cfclnRAYElcs\nqMdXRAHMSbws1daXEqm08M5To9tMbI9SFqXBvktr8WC4BPusfhebKSBrfaIPcZVz\nP6ZGOZet9fPFyY3kmztp0Ncxb2sQVBf+Dsmi58xeATQ2WI+UKDcY27aGVwxOQS75\nu7YOPir77nKugB6nzUGYra6Um3H8hYNWTgWyiATb8Y0V4njCf8pAepGOptClyI1I\ni5fsEE6q52jbGeFRK2JTysG8ovABBdGYsS8XOUuZ+O/QktF/iFwFtMWdEur5tcOO\nRoPSrc/4H8pNpL7IhF0Iy/hpNoNsin7Gj4UBNi6dhrtcGz3zCGSKtldsootgSC2C\nKWd/rAECgYEA5sF6OZsLguVfCqmj3WiLM5I+YWC/HAmV9grb9puW35cQxfQegmdj\nInWk+rcotuFTBcTKjXDKT4C8vCZid2p0WnSWqLPWhPYg0p2awobZgjRy0HzvUgGJ\n/gWAEydzsUc8ojHrUBdJ2iyvjy+I8JWQcyQkBUGlPZj0IC5VUgODYD0CgYEA4Usg\nUCJqo35pLq0TmPSfUuMPzTV3StIft+r7S3g4HWpvrBQNKf6p96/Fjt2WaPhvAABB\nww8Pg2B97iSqR6Rg4Ba4BQQEfHtWCHQ2NuNOoNkRLTJqOxREk7+741Qy9EwgeDJ6\nrQqgrde1dLJPZDzQpbFoCLkIkQ6CL3jTkyDenSECgYEAmvZ1STgoy9eTMsrnY2mw\niYp9X9GjpYV+coOqYfrsn+yH9BfTYUli1qJgj4nuypmYsngMel2zTx6qIEQ6vez8\nhD5lapeSySmssyPp6Ra7/OeR7xbndI/aBn/VGYfV9shbHKUfXGK3Us/Nef+3G7Gl\nFt2/XtRNzobn8rCK1Y/MaxUCgYB6RFpKAxOanS0aLsX2+bNJuX7G4KBYE8cw+i7d\nG2Zg2HW4jr1CMDov+M2fpjRNzZ34AyutX4wMwZ42UuGytcv5cXr3BeIlaI4dUmxl\nx2DRvFwtCjJK08oP4TtnuTdaC8KHWOXo6V6gWfPZXDfn73VQpwIN0dWLW7NdbhZs\nv6bw4QKBgEXYPIf827EVz0XU+1wkjaLt+G40J9sAPk/a6qybF33BBbBhjDxMnest\nArGIjYo4IcYu5hzwnPy/B9WIFgz1iY31l01eP90zJ6q+xpCO5qSdSnjkfq1zrwzK\nBs7B72+hgS7VwRowRUbNanaZIZt0ZAiwQWN1+Dh7Bj+VbSxc/fna\n-----END RSA PRIVATE KEY-----`"
	data       = "IwojIE9DSSBOYXRpdmUgSW5ncmVzcyBDb250cm9sbGVyCiMKIyBDb3B5cmlnaHQgKGMpIDIwMjMgT3JhY2xlIEFtZXJpY2EsIEluYy4gYW5kIGl0cyBhZmZpbGlhdGVzLgojIExpY2Vuc2VkIHVuZGVyIHRoZSBVbml2ZXJzYWwgUGVybWlzc2l2ZSBMaWNlbnNlIHYgMS4wIGFzIHNob3duIGF0IGh0dHBzOi8vb3NzLm9yYWNsZS5jb20vbGljZW5zZXMvdXBsLwojCmF1dGg6CiAgcmVnaW9uOiB1cy1hc2hidXJuLTEKICBwYXNzcGhyYXNlOiBwYXNzCiAgdXNlcjogb2NpZDEudXNlci5vYzEuLmFhYWFhYWFhX2V4YW1wbGUKICBmaW5nZXJwcmludDogNjc6ZDk6NzQ6NGI6MjE6ZXhhbXBsZQogIHRlbmFuY3k6IG9jaWQxLnRlbmFuY3kub2MxLi5hYWFhYWFhYV9leGFtcGxl"
)

func setUp(secret *v1.Secret, setClient bool) *fakeclientset.Clientset {
	client := fakeclientset.NewSimpleClientset()
	if setClient {
		action := "get"
		resource := "secrets"
		obj := secret
		util.FakeClientGetCall(client, action, resource, obj)
	}
	return client
}

func TestGetConfigurationProviderSuccess(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()
	opts := types.IngressOpts{
		AuthType:       "user",
		AuthSecretName: "oci-config",
	}
	configName := "config"
	privateKey := "private-key"
	secret := util.GetSampleSecret(configName, privateKey, data, PrivateKey)
	client := setUp(secret, true)

	auth, err := GetConfigurationProvider(ctx, opts, client)
	Expect(auth != nil).Should(BeTrue())
	Expect(err).Should(BeNil())
}

func TestGetConfigurationProviderFailSecret(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.TODO()
	opts := types.IngressOpts{
		AuthType:       "user",
		AuthSecretName: "oci-config",
	}
	secret := util.GetSampleSecret("test", "error", data, PrivateKey)

	client := setUp(secret, false)
	auth, err := GetConfigurationProvider(ctx, opts, client)
	Expect(auth == nil).Should(BeTrue())
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("error retrieving secret: oci-config"))

	client = setUp(secret, true)
	auth, err = GetConfigurationProvider(ctx, opts, client)
	Expect(auth == nil).Should(BeTrue())
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("auth config data is empty: oci-config"))

	secret = util.GetSampleSecret("config", "error", data, PrivateKey)
	client = setUp(secret, true)
	auth, err = GetConfigurationProvider(ctx, opts, client)
	Expect(auth == nil).Should(BeTrue())
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("missing auth config data: invalid user auth config data: oci-config"))

	secret = util.GetSampleSecret("configs", "error", data, PrivateKey)
	client = setUp(secret, true)
	auth, err = GetConfigurationProvider(ctx, opts, client)
	Expect(auth == nil).Should(BeTrue())
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("auth config data is empty: oci-config"))
}

func TestRetrieveAuthConfigInstanceAuthType(t *testing.T) {
	RegisterTestingT(t)
	opts := types.IngressOpts{
		AuthType: "instance",
	}
	cfg, err := RetrieveAuthConfig(context.TODO(), opts, "test", nil)
	Expect(err == nil).Should(BeTrue())
	Expect(cfg.Type).Should(Equal(types.Instance))

}
func TestRetrieveAuthConfigInstanceAuthTypeTestRetrieveAuthConfigInvalidAuthType(t *testing.T) {
	RegisterTestingT(t)
	authType := "random"
	opts := types.IngressOpts{
		AuthType: authType,
	}
	_, err := RetrieveAuthConfig(context.TODO(), opts, "test", nil)
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal(fmt.Sprintf("invalid auth principal type, %s", authType)))

}

func TestParseAuthConfig(t *testing.T) {
	RegisterTestingT(t)
	configName := "config"
	privateKey := "private-key"
	secret := util.GetSampleSecret(configName, privateKey, data, PrivateKey)
	authCfg, err := ParseAuthConfig(secret, "oci-config")
	Expect(err == nil).Should(BeTrue())
	Expect(authCfg.TenancyID).Should(Equal("ocid1.tenancy.oc1..aaaaaaaa_example"))
	Expect(authCfg.Region).Should(Equal("us-ashburn-1"))
	Expect(authCfg.UserID).Should(Equal("ocid1.user.oc1..aaaaaaaa_example"))
	Expect(authCfg.Fingerprint).Should(Equal("67:d9:74:4b:21:example"))
	err = authCfg.Validate()
	Expect(err == nil).Should(BeTrue())
}

func TestParseAuthConfigWithError(t *testing.T) {
	RegisterTestingT(t)
	secret := util.GetSampleSecret("error", "", data, PrivateKey)
	_, err := ParseAuthConfig(secret, "oci-configs")
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("invalid auth config data: oci-configs"))

	secret = util.GetSampleSecret("config", "", data, PrivateKey)
	_, err = ParseAuthConfig(secret, "oci-configs")
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("invalid user auth config data: oci-configs"))

}

func TestSetHTTPClientTimeout(t *testing.T) {
	RegisterTestingT(t)
	timeout := setHTTPClientTimeout(httpClientTimeout)
	Expect(timeout != nil).Should(Equal(true))
	dis, err := timeout(&http.Client{})
	Expect(dis).Should(Not(BeNil()))
	Expect(err).Should(BeNil())

	dis, err = timeout(nil)
	Expect(dis).Should(BeNil())
	Expect(err).Should(Not(BeNil()))
	Expect(err.Error()).Should(Equal("unable to modify unknown HTTP client type"))
}
