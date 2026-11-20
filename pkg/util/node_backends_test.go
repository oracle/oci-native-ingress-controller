package util

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeBackendsStableCap(t *testing.T) {
	nodes := make([]*corev1.Node, 0, 10)
	for i := 0; i < 10; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%d", i)},
			Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: fmt.Sprintf("10.0.0.%d", i+1)}}},
		})
	}
	first := NodeBackends(nodes, "backend-set", 30000, 4)
	if len(first) != 4 {
		t.Fatalf("got %d backends, want 4", len(first))
	}
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
	second := NodeBackends(nodes, "backend-set", 30000, 4)
	for i := range first {
		if *first[i].IpAddress != *second[i].IpAddress || *first[i].Port != *second[i].Port {
			t.Fatalf("node selection changed with informer order: %v versus %v", first, second)
		}
	}
	if got := len(NodeBackends(nodes, "backend-set", 30000, 0)); got != len(nodes) {
		t.Fatalf("unlimited mode selected %d of %d nodes", got, len(nodes))
	}
}
