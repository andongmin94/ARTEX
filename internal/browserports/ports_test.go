package browserports

import "testing"

func TestHTTPPortBoundary(t *testing.T) {
	for _, port := range []int{-1, 0, 1, 21, 6000, 6667, 10080, 65536} {
		if Allowed(port) {
			t.Errorf("unsafe port %d accepted", port)
		}
	}
	for _, port := range []int{80, 443, 8787, 10081, 49152, 65535} {
		if !Allowed(port) {
			t.Errorf("safe port %d rejected", port)
		}
	}
}
