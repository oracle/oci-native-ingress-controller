package server

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/oracle/oci-go-sdk/v65/containerengine"
)

func TestKubeConfig(t *testing.T) {
	RegisterTestingT(t)
	cfg, err := BuildConfig("config")
	Expect(err == nil && cfg != nil).Should(Equal(true))
	Expect(cfg.Host).Should(Equal("https://129.80.70.29:6443"))
}

func TestGetCniFromCluster(t *testing.T) {
	RegisterTestingT(t)
	ns := make([]ClusterPodNetworkOptionDetails, 1)

	// No value test
	resp := Cluster{
		ClusterPodNetworkOptions: ns,
	}
	cni, err := GetCniFromCluster(resp)
	Expect(cni).Should(Equal(string(ClusterPodNetworkOptionDetailsCniTypeFlannelOverlay)))
	Expect(err).Should(BeNil())
}
