package metric

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestSyncErrorCounters(t *testing.T) {
	reg := prometheus.NewRegistry()
	collector := NewIngressCollector("test-controller-class", reg)
	controllers := []string{ControllerIngress, ControllerIngressClass, ControllerRoutingPolicy, ControllerBackend}
	check := func(incremented bool) {
		t.Helper()
		response := httptest.NewRecorder()
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, "# TYPE oci_ingress_sync_errors_total counter\n") || strings.Count(body, "\noci_ingress_sync_errors_total{") != 4 {
			t.Fatalf("expected four counter series, got:\n%s", body)
		}
		for i, controller := range controllers {
			want := 0
			if incremented {
				want = i + 1
			}
			line := fmt.Sprintf("oci_ingress_sync_errors_total{controller=%q,controller_class=\"test-controller-class\"} %d\n", controller, want)
			if !strings.Contains(body, line) {
				t.Fatalf("missing %q in metrics:\n%s", line, body)
			}
		}
	}
	check(false)
	for i, controller := range controllers {
		for n := 0; n <= i; n++ {
			collector.IncrementSyncErrors(controller)
		}
	}
	check(true)
}

func TestSyncErrorCounterWithoutMetrics(t *testing.T) {
	collector := NewIngressCollector("test", nil)
	collector.IncrementSyncErrors(ControllerBackend)
}
