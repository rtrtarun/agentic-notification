package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewServiceDefaultsBaseURL(t *testing.T) {
	if NewService("").baseURL != "http://localhost:11434" {
		t.Fatal("empty base URL must default to local Ollama")
	}
}

func TestSummarizeReportsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := NewService(srv.URL).Summarize(context.Background(), "text")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestSummarizeRejectsMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()
	if _, err := NewService(srv.URL).Summarize(context.Background(), "text"); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestSummarizeReportsUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := NewService(url).Summarize(context.Background(), "text"); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestSummarizeRejectsBadBaseURL(t *testing.T) {
	if _, err := NewService("http://bad host").Summarize(context.Background(), "text"); err == nil {
		t.Fatal("expected request build error")
	}
}
