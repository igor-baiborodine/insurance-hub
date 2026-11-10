package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckHTTPRequiresPolicyResult(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Query().Get("q") != "Synthetic Person" {
				t.Errorf("query = %q", request.URL.Query().Get("q"))
			}
			_, _ = response.Write(
				[]byte(`{"policies":[{"number":"POLICY-SYNTHETIC-001"}]}`),
			)
		}),
	)
	defer server.Close()

	if err := checkHTTP(
		context.Background(),
		server.URL+"/policies",
		"Synthetic Person",
	); err != nil {
		t.Fatal(err)
	}
}

func TestRunCheckRejectsUnsupportedMode(t *testing.T) {
	if err := runCheck(
		context.Background(),
		options{mode: "migration", address: "unused"},
	); err == nil {
		t.Fatal("unsupported mode was accepted")
	}
}
