package types

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestAuthConfig_Validate(t *testing.T) {
	RegisterTestingT(t)
	cfg := &AuthConfig{}
	err := validateConfig(cfg)
	Expect(len(err)).Should(Equal(5))
}

func TestMapToPrincipalType(t *testing.T) {
	RegisterTestingT(t)
	principal, err := MapToPrincipalType("instance")
	Expect(err == nil).Should(BeTrue())
	Expect(principal).Should(Equal(Instance))

	principal, err = MapToPrincipalType("user")
	Expect(err == nil).Should(BeTrue())
	Expect(principal).Should(Equal(User))

	principal, err = MapToPrincipalType("workloadIdentity")
	Expect(err == nil).Should(BeTrue())
	Expect(principal).Should(Equal(WorkloadIdentity))

	_, err = MapToPrincipalType("random")
	Expect(err != nil).Should(BeTrue())
	Expect(err.Error()).Should(Equal("unknown OCI principal type: random"))
}

func TestValidate(t *testing.T) {
	RegisterTestingT(t)
	cfg := &AuthConfig{}
	err := cfg.Validate()
	Expect(err != nil).Should(BeTrue())

	cfg = &AuthConfig{
		Region:      "ashburn",
		TenancyID:   "98123821389",
		UserID:      "12321312",
		PrivateKey:  "123213",
		Fingerprint: "12321312",
		Passphrase:  "123213",
	}

	err = cfg.Validate()
	Expect(err == nil).Should(BeTrue())
}
