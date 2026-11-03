package util

import (
	"crypto/sha256"
	"sort"

	ociloadbalancer "github.com/oracle/oci-go-sdk/v65/loadbalancer"
	corev1 "k8s.io/api/core/v1"
)

// NodeBackends returns node-port backends for a Cluster traffic policy Service.
// A zero limit retains the existing all-node behavior. When limited, the
// backend-set-specific ranking is stable across informer listing order and
// changes only when nodes enter or leave the eligible set.
func NodeBackends(nodes []*corev1.Node, backendSetName string, nodePort int32, limit int) []ociloadbalancer.BackendDetails {
	type candidate struct {
		name string
		ip   string
	}
	candidates := make([]candidate, 0, len(nodes))
	for _, node := range nodes {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP && address.Address != "" {
				candidates = append(candidates, candidate{name: node.Name, ip: address.Address})
				break
			}
		}
	}
	if limit > 0 && len(candidates) > limit {
		sort.Slice(candidates, func(i, j int) bool {
			left := sha256.Sum256([]byte(backendSetName + "/" + candidates[i].name))
			right := sha256.Sum256([]byte(backendSetName + "/" + candidates[j].name))
			return string(left[:]) < string(right[:])
		})
		candidates = candidates[:limit]
	}

	backends := make([]ociloadbalancer.BackendDetails, 0, len(candidates))
	for _, candidate := range candidates {
		backends = append(backends, NewBackend(candidate.ip, nodePort))
	}
	return backends
}
