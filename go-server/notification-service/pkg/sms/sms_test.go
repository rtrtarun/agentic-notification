package sms

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidNumber(t *testing.T) {
	for _, ok := range []string{"+14155550100", "+447911123456"} {
		if !ValidNumber(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "14155550100", "+0123456789", "+1415 555 0100", "+1", "+1415555010011111", "+1415555010a"} {
		if ValidNumber(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestMaskNumber(t *testing.T) {
	if got := MaskNumber("+14155550100"); got != "+14*******00" {
		t.Fatalf("mask = %q", got)
	}
	if MaskNumber("+1") != "***" {
		t.Fatal("short numbers are fully masked")
	}
}

func TestMockRecordsAndValidates(t *testing.T) {
	m := NewMock(nil)
	if err := m.Send(context.Background(), "+14155550100", "hi"); err != nil {
		t.Fatal(err)
	}
	if got := m.Sent(); len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("sent = %+v", got)
	}
	if err := m.Send(context.Background(), "nope", "x"); err != ErrInvalidNumber {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m.Send(ctx, "+14155550100", "x") == nil {
		t.Fatal("a cancelled context must fail")
	}
}

func TestHTTPGatewayPostsEscapedTemplate(t *testing.T) {
	var gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotAuth = string(b), r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	g, err := NewHTTP(srv.URL, map[string]string{"Authorization": "Bearer tok"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Send(context.Background(), "+14155550100", `say "hi"`+"\n"); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"to":"+14155550100","text":"say \"hi\"\n"}` {
		t.Fatalf("body = %s", gotBody)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

func TestHTTPGatewayErrorsHideSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("token abc and number +14155550100"))
	}))
	defer srv.Close()
	g, _ := NewHTTP(srv.URL+"?token=SECRET", nil, "")
	err := g.Send(context.Background(), "+14155550100", "x")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "abc") || strings.Contains(err.Error(), "4155550100") {
		t.Fatalf("error leaks the response body: %v", err)
	}

	srv.Close()
	err = g.Send(context.Background(), "+14155550100", "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("connection error must not repeat the URL: %v", err)
	}
}

func TestHTTPGatewayRejectsBadInput(t *testing.T) {
	if _, err := NewHTTP("ftp://x", nil, ""); err == nil {
		t.Fatal("non-http URL must be rejected")
	}
	if _, err := NewHTTP("", nil, ""); err == nil {
		t.Fatal("empty URL must be rejected")
	}
	g, _ := NewHTTP("http://localhost", nil, "")
	if g.Send(context.Background(), "bad", "x") != ErrInvalidNumber {
		t.Fatal("bad number must be rejected before any request")
	}
}

// The vanilla "get-vanilla" vector from the AWS Signature Version 4 test suite.
func TestSignV4MatchesAWSVector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	signV4(req, nil, "service", "us-east-1",
		Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
		time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func TestSNSPublishesSignedForm(t *testing.T) {
	var form url.Values
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form, auth = r.PostForm, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g, err := NewSNS("eu-west-1", Credentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "TOK"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Send(context.Background(), "+14155550100", "hello"); err != nil {
		t.Fatal(err)
	}
	if form.Get("Action") != "Publish" || form.Get("PhoneNumber") != "+14155550100" || form.Get("Message") != "hello" {
		t.Fatalf("form = %v", form)
	}
	if !strings.Contains(auth, "Credential=AK/") || !strings.Contains(auth, "/eu-west-1/sns/aws4_request") ||
		!strings.Contains(auth, "x-amz-security-token") {
		t.Fatalf("authorization = %s", auth)
	}
}

func TestSNSNeedsCredentialsAndReportsStatus(t *testing.T) {
	if _, err := NewSNS("", Credentials{}, ""); err == nil {
		t.Fatal("missing credentials must be rejected")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	g, _ := NewSNS("us-east-1", Credentials{AccessKeyID: "a", SecretAccessKey: "b"}, srv.URL)
	if err := g.Send(context.Background(), "+14155550100", "x"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v", err)
	}
	if g.Send(context.Background(), "bad", "x") != ErrInvalidNumber {
		t.Fatal("bad number")
	}
}
