package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials are AWS access keys. SessionToken is set for temporary credentials.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// SNSGateway publishes text messages through Amazon SNS using the Query API
// signed with Signature Version 4, so no AWS SDK is needed.
type SNSGateway struct {
	region   string
	creds    Credentials
	endpoint string
	client   *http.Client
	now      func() time.Time
}

// NewSNS builds an SNS gateway. endpoint is optional and only for tests or VPC endpoints.
func NewSNS(region string, creds Credentials, endpoint string) (*SNSGateway, error) {
	if region == "" || creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return nil, errors.New("sns needs AWS_REGION, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	if endpoint == "" {
		endpoint = "https://sns." + region + ".amazonaws.com/"
	}
	return &SNSGateway{
		region: region, creds: creds, endpoint: endpoint, now: time.Now,
		client: &http.Client{
			Timeout:       httpTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Name identifies the gateway.
func (g *SNSGateway) Name() string { return "sns" }

// Send publishes one transactional SMS.
func (g *SNSGateway) Send(ctx context.Context, to, text string) error {
	if !ValidNumber(to) {
		return ErrInvalidNumber
	}
	form := url.Values{}
	form.Set("Action", "Publish")
	form.Set("Version", "2010-03-31")
	form.Set("PhoneNumber", to)
	form.Set("Message", text)
	form.Set("MessageAttributes.entry.1.Name", "AWS.SNS.SMS.SMSType")
	form.Set("MessageAttributes.entry.1.Value.DataType", "String")
	form.Set("MessageAttributes.entry.1.Value.StringValue", "Transactional")
	body := form.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build sns request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signV4(req, []byte(body), "sns", g.region, g.creds, g.now())

	resp, err := g.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("send sns: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError("sns", resp.StatusCode)
	}
	return nil
}

// signV4 adds the X-Amz-Date, optional security token and Authorization
// headers. Only host, content-type, x-amz-date and x-amz-security-token are
// signed, and only when present.
func signV4(req *http.Request, body []byte, service, region string, c Credentials, t time.Time) {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	}

	headers := map[string]string{"host": req.URL.Host, "x-amz-date": amzDate}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		headers["content-type"] = ct
	}
	if c.SessionToken != "" {
		headers["x-amz-security-token"] = c.SessionToken
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{
		req.Method, path, req.URL.RawQuery, canonHeaders.String(), signed, hexSHA256(body),
	}, "\n")

	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256([]byte(canonical))

	key := hmacSHA256([]byte("AWS4"+c.SecretAccessKey), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.AccessKeyID, scope, signed, signature))
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
