package speech

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetcherDownloadsPinnedHTTPSArtifact(t *testing.T) {
	body := "verified artifact"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "17")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	var output bytes.Buffer
	err := HTTPFetcher(server.Client())(context.Background(), Artifact{URL: server.URL, Size: int64(len(body))}, &output)
	if err != nil || output.String() != body {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}

func TestHTTPFetcherRejectsUnsafeAndInvalidResponses(t *testing.T) {
	if err := HTTPFetcher(nil)(context.Background(), Artifact{URL: "http://example.test", Size: 1}, &bytes.Buffer{}); err == nil {
		t.Fatal("HTTP URL accepted")
	}
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("xx"))
		}))
		err := HTTPFetcher(server.Client())(context.Background(), Artifact{URL: server.URL, Size: 1}, &bytes.Buffer{})
		server.Close()
		if err == nil {
			t.Fatalf("status %d or length mismatch accepted", status)
		}
	}
}

func TestHTTPFetcherRejectsUnsafeRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "http://example.test/artifact", http.StatusFound)
	}))
	defer server.Close()
	err := HTTPFetcher(server.Client())(context.Background(), Artifact{URL: server.URL, Size: 1}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "download artifact") {
		t.Fatalf("err=%v", err)
	}
}
