package line

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

// Every request is intercepted in memory. No DNS, sockets, or real credentials.
func auditResponse(r *http.Request, status int, body io.Reader) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(body), Request: r}
}

func TestAuthenticatedRequestsRejectRedirects(t *testing.T) {
	for _, path := range []string{"rpc", "obs", "sse"} {
		for _, target := range []string{"https://audit.invalid/collect", "http://audit.invalid/collect"} {
			t.Run(path+"/"+strings.Split(target, ":")[0], func(t *testing.T) {
				c := NewClient("synthetic-audit-token")
				var redirected, credential, bodyReplayed bool
				transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host == "audit.invalid" {
						redirected = true
						credential = r.Header.Get("x-line-access") != ""
						if r.Body != nil {
							b, _ := io.ReadAll(r.Body)
							bodyReplayed = len(b) > 0
						}
						return auditResponse(r, 200, strings.NewReader(`{"code":0,"data":{}}`)), nil
					}
					res := auditResponse(r, 307, strings.NewReader(""))
					res.Header.Set("Location", target)
					return res, nil
				})
				c.HTTPClient.Transport = transport
				c.OBSClient.Transport = transport
				switch path {
				case "rpc":
					_, _ = c.callRPCContext(context.Background(), "TalkService", "sendMessage", 1, map[string]string{"text": "synthetic"})
				case "obs":
					_, _ = c.downloadOBSObject(context.Background(), OBSBaseURL+"/r/talk/m/synthetic", "synthetic-obs-token", "123", 1024)
				case "sse":
					old := sseHTTPClient
					t.Cleanup(func() { sseHTTPClient = old })
					copy := *sseHTTPClient
					copy.Transport = transport
					sseHTTPClient = &copy
					_ = c.ListenSSE(context.Background(), 0, func(string, string) {})
				}
				if redirected {
					t.Fatalf("unsafe redirect followed: credential_forwarded=%t request_body_replayed=%t", credential, bodyReplayed)
				}
			})
		}
	}
}

func TestMutationRedirectDoesNotReplay(t *testing.T) {
	c := NewClient("synthetic-audit-token")
	dispatches := 0
	c.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				dispatches++
			}
		}
		if dispatches == 1 {
			res := auditResponse(r, 307, strings.NewReader(""))
			res.Header.Set("Location", r.URL.String()+"?redirected=1")
			return res, nil
		}
		return auditResponse(r, 200, strings.NewReader(`{"code":0,"data":{}}`)), nil
	})
	_, _ = c.callRPCContext(context.Background(), "TalkService", "sendMessage", 1, map[string]string{"text": "synthetic"})
	if dispatches != 1 {
		t.Fatalf("one mutation call dispatched its POST body %d times", dispatches)
	}
}

func TestRedirectPolicyOverridesCallerAndPreservesClient(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, target := range []string{"https://source.invalid/next", "https://other.invalid/collect", "http://other.invalid/collect", "https://other.invalid/chain"} {
			t.Run(fmt.Sprintf("%d/%s", status, target), func(t *testing.T) {
				dispatches, callbacks, closed := 0, 0, false
				jar, _ := cookiejar.New(nil)
				transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
					dispatches++
					if dispatches != 1 {
						t.Fatal("redirect dispatched private headers/body")
					}
					resp := auditResponse(r, status, strings.NewReader("private raw response"))
					resp.Body = &policyBody{Reader: strings.NewReader("private raw response"), closed: &closed}
					resp.Header.Set("Location", target)
					return resp, nil
				})
				client := &http.Client{Transport: transport, Jar: jar, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { callbacks++; return nil }}
				req, _ := http.NewRequest("POST", "https://source.invalid/original", strings.NewReader("private mutation"))
				for _, key := range []string{"x-line-access", "Cookie", "x-hmac", "x-talk-meta"} {
					req.Header.Set(key, "synthetic")
				}
				_, err := doLINERequest(client, req)
				if !errors.Is(err, ErrRedirectRejected) || IsAuthoritativeRejection(err) || dispatches != 1 || callbacks != 0 || !closed {
					t.Fatal("redirect was not safely rejected")
				}
				if client.Jar != jar || client.Timeout != time.Second || client.Transport == nil {
					t.Fatal("caller client settings changed")
				}
				_ = client.CheckRedirect(nil, nil)
				if callbacks != 1 {
					t.Fatal("caller redirect callback mutated")
				}
			})
		}
	}
}

type policyBody struct {
	io.Reader
	closed *bool
}

func (b *policyBody) Close() error { *b.closed = true; return nil }
