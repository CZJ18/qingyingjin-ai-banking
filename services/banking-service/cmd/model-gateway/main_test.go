package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeAcceptsActualGatewayReadinessWithoutConfigurationOrDatabase(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Error("wrong readiness route")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer s.Close()
	if e := run([]string{"--probe", s.URL + "/readyz"}); e != nil {
		t.Fatal(e)
	}
}
func TestProbeRejectsUnreadyAndNonLoopbackTargets(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer s.Close()
	for _, target := range []string{s.URL + "/readyz", "http://unapproved.example:8092/readyz", s.URL + "/readyz?token=do-not-send", s.URL + "/other"} {
		if run([]string{"--probe", target}) == nil {
			t.Fatal("untrusted or unready probe accepted")
		}
	}
}
