package line

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOBSTokenCacheScopedPerClient(t *testing.T) {
	callsA, callsB := 0, 0
	makeClient := func(token string, obsToken string, counter *int) *Client {
		c := NewClient(token)
		c.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*counter++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"OK","data":"3600\u001e` + obsToken + `"}`)),
			}, nil
		})}
		return c
	}

	clientA := makeClient("line-token-a", "obs-token-a", &callsA)
	clientB := makeClient("line-token-b", "obs-token-b", &callsB)

	gotA1, err := clientA.AcquireEncryptedAccessTokenContext(context.Background())
	if err != nil || gotA1 != "obs-token-a" || callsA != 1 {
		t.Fatalf("clientA first acquire = (%q, %v), calls=%d", gotA1, err, callsA)
	}
	gotB1, err := clientB.AcquireEncryptedAccessTokenContext(context.Background())
	if err != nil || gotB1 != "obs-token-b" || callsB != 1 {
		t.Fatalf("clientB first acquire = (%q, %v), calls=%d (must not reuse clientA cache)", gotB1, err, callsB)
	}
	gotA2, err := clientA.AcquireEncryptedAccessTokenContext(context.Background())
	if err != nil || gotA2 != "obs-token-a" || callsA != 1 {
		t.Fatalf("clientA cached acquire = (%q, %v), calls=%d", gotA2, err, callsA)
	}
}

func TestWaitForLoginJQUsesReturnedVerifierAndClassifiesExpired410(t *testing.T) {
	t.Run("uses returned JQ verifier for LoginV2WithVerifier", func(t *testing.T) {
		c := NewClient("")
		var gotLoginVerifier string
		c.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/api/talk/long-polling/JQ") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"code":0,"message":"OK","data":{"result":{"verifier":"returned-jq-verifier","authPhase":"QRCODE_VERIFIED"}}}`,
					)),
				}, nil
			}
			if strings.HasSuffix(req.URL.Path, "/AuthService/loginV2") {
				var args []LoginRequest
				b, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(b, &args)
				if len(args) > 0 {
					gotLoginVerifier = args[0].Verifier
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"OK","data":{"authToken":"tok"}}`)),
				}, nil
			}
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		})}

		res, err := c.waitForLoginJQ("initial-verifier")
		if err != nil || res == nil || !res.NoE2EE {
			t.Fatalf("waitForLoginJQ failed: res=%+v err=%v", res, err)
		}
		if gotLoginVerifier != "returned-jq-verifier" {
			t.Fatalf("LoginV2WithVerifier verifier = %q, want returned-jq-verifier", gotLoginVerifier)
		}
	})

	t.Run("gateway 10052 statusCode 410 classifies as ErrLoginVerificationExpired", func(t *testing.T) {
		for _, noE2EE := range []bool{false, true} {
			c := NewClient("")
			c.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"code":10052,"message":"RESPONSE_HTTP_ERROR","data":{"statusCode":410,"secret":"never-leak"}}`,
					)),
				}, nil
			})}
			_, err := c.WaitForLogin("initial-verifier", noE2EE)
			if !errors.Is(err, ErrLoginVerificationExpired) {
				t.Fatalf("noE2EE=%v err = %v, want ErrLoginVerificationExpired", noE2EE, err)
			}
			if strings.Contains(err.Error(), "never-leak") {
				t.Fatalf("noE2EE=%v leaked response body: %v", noE2EE, err)
			}
		}
	})
}
