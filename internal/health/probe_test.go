package health

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbe(t *testing.T) {
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer ts.Close()
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())

	// A wildcard bind address must be probed via loopback.
	if err := Probe("0.0.0.0:"+port, "8080"); err != nil {
		t.Fatalf("expected healthy: %v", err)
	}
	if err := Probe(":"+port, "8080"); err != nil {
		t.Fatalf("expected healthy for :port form: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := Probe(":"+port, "8080"); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
	if err := Probe("not-an-address", "8080"); err == nil {
		t.Fatal("expected an error for a malformed address")
	}
	ts.Close()
	if err := Probe(":"+port, "8080"); err == nil {
		t.Fatal("expected an error when nothing is listening")
	}
}
