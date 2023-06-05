package server

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestKubeConfig(t *testing.T) {
	RegisterTestingT(t)
	cfg, err := BuildConfig("config")
	Expect(err == nil && cfg != nil).Should(Equal(true))
	Expect(cfg.Host).Should(Equal("https://129.80.70.29:6443"))
}
