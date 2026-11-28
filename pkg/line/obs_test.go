package line

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type observedOBSRequest struct {
	path    string
	query   string
	headers http.Header
}

func installCachedOBSToken(client *Client) {
	client.obsTokenMu.Lock()
	client.obsTokenCache = "obs-token"
	client.obsTokenExpiry = time.Now().Add(time.Hour)
	client.obsTokenMu.Unlock()
}

func obsResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDownloadOBSHonorsOptionalSizeLimit(t *testing.T) {
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "object_info.obs") {
			return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
		}
		return obsResponse(http.StatusOK, "12345"), nil
	})}
	if _, err := client.DownloadOBSWithSIDOptions(context.Background(), "oid", "123", "emf", OBSDownloadOptions{MaxBytes: 4}); !errors.Is(err, ErrOBSSizeLimit) {
		t.Fatal("oversized object accepted")
	}
	data, err := client.DownloadOBSWithSIDOptions(context.Background(), "oid", "123", "emf", OBSDownloadOptions{MaxBytes: 5})
	if err != nil || string(data) != "12345" {
		t.Fatal("exact-limit object failed", err)
	}
	data, err = client.DownloadOBSWithSIDOptions(context.Background(), "oid", "123", "emf", OBSDownloadOptions{})
	if err != nil || string(data) != "12345" {
		t.Fatal("unbounded download behavior changed", err)
	}
}

func TestDownloadOBSPlainMatchesChromeRequestFlow(t *testing.T) {

	var requests []observedOBSRequest
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, observedOBSRequest{
				path:    req.URL.Path,
				query:   req.URL.RawQuery,
				headers: req.Header.Clone(),
			})
			if len(requests) == 1 {
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			}
			return obsResponse(http.StatusOK, "image-bytes"), nil
		}),
	}

	data, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("data = %q, want image-bytes", data)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want object-info preflight plus download", len(requests))
	}
	if requests[0].path != "/r/talk/m/message-id/object_info.obs" || requests[1].path != "/r/talk/m/message-id" {
		t.Fatalf("request paths = %q / %q", requests[0].path, requests[1].path)
	}
	for i, req := range requests {
		if req.query != "" {
			t.Fatalf("request %d query = %q, want empty", i, req.query)
		}
		if req.headers.Get("X-Line-Access") != "obs-token" {
			t.Fatalf("request %d X-Line-Access = %q", i, req.headers.Get("X-Line-Access"))
		}
		if req.headers.Get("X-Line-Application") != lineApplicationHeader {
			t.Fatalf("request %d X-Line-Application = %q, want %q", i, req.headers.Get("X-Line-Application"), lineApplicationHeader)
		}
		if req.headers.Get("X-Talk-Meta") != "" {
			t.Fatalf("plain request %d unexpectedly sent X-Talk-Meta", i)
		}
	}
}

func TestDownloadOBSPublicResourceMatchesChromeRequestFlow(t *testing.T) {
	var request *http.Request
	client := NewClient("line-token-that-must-not-be-used")
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			request = req
			return obsResponse(http.StatusOK, "business-image"), nil
		}),
	}

	data, err := client.DownloadOBSPublicResource(
		context.Background(),
		"/r/official/image-id?public=resource",
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "business-image" {
		t.Fatalf("data = %q, want business-image", data)
	}
	if request == nil {
		t.Fatal("public resource request was not made")
	}
	if request.URL.Scheme != "https" || request.URL.Host != "obs.line-apps.com" {
		t.Fatalf("request URL origin = %s://%s", request.URL.Scheme, request.URL.Host)
	}
	if request.URL.Path != "/r/official/image-id" || request.URL.RawQuery != "public=resource" {
		t.Fatalf("request URL = %s", request.URL.String())
	}
	if request.Header.Get("X-Line-Access") != "" {
		t.Fatal("public resource request unexpectedly included X-Line-Access")
	}
	if request.Header.Get("X-Line-Application") != "" {
		t.Fatal("public resource request unexpectedly included X-Line-Application")
	}
	if request.Header.Get("X-Talk-Meta") != "" {
		t.Fatal("public resource request unexpectedly included X-Talk-Meta")
	}
}

func TestDownloadOBSPublicResourceRejectsUnsafeURLs(t *testing.T) {
	client := NewClient("line-token")
	for _, resourcePath := range []string{
		"//example.com/image",
		"relative/image",
		"ftp://example.com/image",
		"https://127.0.0.1/image",
		"https://user:pass@example.com/image",
		"https://example.com:8443/image",
		"https://example.com/image#frag",
	} {
		if _, err := client.DownloadOBSPublicResource(context.Background(), resourcePath); err == nil {
			t.Fatalf("resource path %q was accepted", resourcePath)
		}
	}
}

func TestDownloadOBSPublicResourceClassifiesMissingObject(t *testing.T) {
	client := NewClient("line-token")
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return obsResponse(http.StatusNotFound, "not found"), nil
		}),
	}

	_, err := client.DownloadOBSPublicResource(context.Background(), "/r/official/missing")
	if !errors.Is(err, ErrOBSObjectNotFound) {
		t.Fatalf("err = %v, want ErrOBSObjectNotFound", err)
	}
}

func TestDownloadOBSResourceUsesReceiveServiceAndSID(t *testing.T) {

	var requests []observedOBSRequest
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, observedOBSRequest{
				path:    req.URL.Path,
				headers: req.Header.Clone(),
			})
			if len(requests) == 1 {
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			}
			return obsResponse(http.StatusOK, "album-image"), nil
		}),
	}

	data, err := client.DownloadOBSResource(context.Background(), "album", "a", "preview-oid", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "album-image" {
		t.Fatalf("data = %q, want album-image", data)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want object-info preflight plus download", len(requests))
	}
	if requests[0].path != "/r/album/a/preview-oid/object_info.obs" || requests[1].path != "/r/album/a/preview-oid" {
		t.Fatalf("request paths = %q / %q", requests[0].path, requests[1].path)
	}
	for i, req := range requests {
		if req.headers.Get("X-Line-Access") != "obs-token" {
			t.Fatalf("request %d X-Line-Access = %q", i, req.headers.Get("X-Line-Access"))
		}
		if req.headers.Get("X-Line-Application") != lineApplicationHeader {
			t.Fatalf("request %d X-Line-Application = %q, want %q", i, req.headers.Get("X-Line-Application"), lineApplicationHeader)
		}
		if req.headers.Get("X-Talk-Meta") != "" {
			t.Fatalf("album request %d unexpectedly sent X-Talk-Meta", i)
		}
	}
}

func TestDownloadOBSPreflightsBaseObjectBeforeTID(t *testing.T) {

	var requests []observedOBSRequest
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, observedOBSRequest{path: req.URL.Path, query: req.URL.RawQuery})
			if len(requests) == 1 {
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			}
			return obsResponse(http.StatusOK, "image-bytes"), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{
		TID:    "original",
		OBSPop: "pop-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want object-info preflight plus download", len(requests))
	}
	if requests[0].path != "/r/talk/m/message-id/object_info.obs" {
		t.Fatalf("object-info path = %q, want base object path", requests[0].path)
	}
	if requests[1].path != "/r/talk/m/message-id/original" {
		t.Fatalf("download path = %q, want TID-specific path", requests[1].path)
	}
	for i, request := range requests {
		if request.query != "p=pop-value" {
			t.Fatalf("request %d query = %q, want OBS pop query", i, request.query)
		}
	}
}

func TestDownloadOBSEncryptedIncludesTalkMeta(t *testing.T) {

	var talkMetaHeaders []string
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			talkMetaHeaders = append(talkMetaHeaders, req.Header.Get("X-Talk-Meta"))
			if len(talkMetaHeaders) == 1 {
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			}
			return obsResponse(http.StatusOK, "encrypted-image-bytes"), nil
		}),
	}

	_, err := client.DownloadOBSWithOptions(context.Background(), "message-id", "message-id", OBSDownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(talkMetaHeaders) != 2 {
		t.Fatalf("requests = %d, want object-info preflight plus download", len(talkMetaHeaders))
	}
	for i, talkMeta := range talkMetaHeaders {
		if talkMeta == "" {
			t.Fatalf("encrypted request %d omitted X-Talk-Meta", i)
		}
	}
}

func TestDownloadOBSWaitsForObjectEncoding(t *testing.T) {
	oldDelay := obsRetryDelay
	obsRetryDelay = 0
	t.Cleanup(func() { obsRetryDelay = oldDelay })

	var paths []string
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			paths = append(paths, req.URL.Path)
			switch len(paths) {
			case 1:
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"ing"}`), nil
			case 2:
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			default:
				return obsResponse(http.StatusOK, "ready"), nil
			}
		}),
	}

	data, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ready" {
		t.Fatalf("data = %q, want ready", data)
	}
	want := []string{
		"/r/talk/m/message-id/object_info.obs",
		"/r/talk/m/message-id/object_info.obs",
		"/r/talk/m/message-id",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestDownloadOBSClassifiesMissingObject(t *testing.T) {

	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return obsResponse(http.StatusOK, `{"status":"notexist"}`), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if !errors.Is(err, ErrOBSObjectNotFound) {
		t.Fatalf("err = %v, want ErrOBSObjectNotFound", err)
	}
}

func TestDownloadOBSDoesNotCallObjectInfoHTTP404Expired(t *testing.T) {

	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return obsResponse(http.StatusNotFound, "not found"), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if err == nil {
		t.Fatal("expected object-info request error")
	}
	if errors.Is(err, ErrOBSObjectNotFound) {
		t.Fatalf("err = %v, raw HTTP failure must not be materialized as known expiry", err)
	}
}

func TestDownloadOBSClassifiesObjectInfoUnauthorized(t *testing.T) {

	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return obsResponse(http.StatusUnauthorized, "unauthorized"), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if !IsUnauthorizedStatus(err) {
		t.Fatalf("err = %v, want unauthorized status for token recovery", err)
	}
}

func TestDownloadOBSDoesNotCallPostPreflight404Expired(t *testing.T) {

	var requests int
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			}
			return obsResponse(http.StatusNotFound, "not found"), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if err == nil {
		t.Fatal("expected download error")
	}
	if errors.Is(err, ErrOBSObjectNotFound) {
		t.Fatalf("err = %v, post-preflight race must not be materialized as known expiry", err)
	}
}

func TestDownloadOBSClassifiesEncodingRetryExhaustion(t *testing.T) {
	oldDelay := obsRetryDelay
	obsRetryDelay = 0
	t.Cleanup(func() { obsRetryDelay = oldDelay })

	var requests int
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"ing"}`), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "message-id", "", "m", OBSDownloadOptions{})
	if !errors.Is(err, ErrOBSEncodingIncomplete) {
		t.Fatalf("err = %v, want ErrOBSEncodingIncomplete", err)
	}
	if requests != obsMaxRetries+1 {
		t.Fatalf("requests = %d, want %d", requests, obsMaxRetries+1)
	}
}

func TestDownloadAlbumPreviewMatchesLINERequestContract(t *testing.T) {
	var channelRequests int
	client := NewClient("line-token")
	client.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			channelRequests++
			if req.URL.Path != "/api/talk/thrift/Talk/ChannelService/issueChannelToken" {
				t.Fatalf("channel token path = %q", req.URL.Path)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != `["1341209850"]` {
				t.Fatalf("channel token body = %q", body)
			}
			return obsResponse(
				http.StatusOK,
				`{"code":0,"message":"OK","data":{"channelAccessToken":"channel-token","expiration":9999999999999}}`,
			), nil
		}),
	}

	var requests []observedOBSRequest
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, observedOBSRequest{
				path:    req.URL.Path,
				query:   req.URL.RawQuery,
				headers: req.Header.Clone(),
			})
			return obsResponse(http.StatusOK, "album-thumbnail"), nil
		}),
	}

	for range 2 {
		data, err := client.DownloadAlbumPreview(
			context.Background(),
			"preview-oid",
			"chat-id",
			"album-id",
		)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "album-thumbnail" {
			t.Fatalf("data = %q, want album-thumbnail", data)
		}
	}

	if channelRequests != 1 {
		t.Fatalf("channel token requests = %d, want one cached request", channelRequests)
	}
	if len(requests) != 2 {
		t.Fatalf("OBS requests = %d, want two direct thumbnail downloads", len(requests))
	}
	for index, request := range requests {
		if request.path != "/r/album/a/preview-oid/f482x482" {
			t.Fatalf("request %d path = %q", index, request.path)
		}
		if request.query != "" {
			t.Fatalf("request %d query = %q, want empty", index, request.query)
		}
		if request.headers.Get("X-Line-ChannelToken") != "channel-token" {
			t.Fatalf("request %d channel token = %q", index, request.headers.Get("X-Line-ChannelToken"))
		}
		if request.headers.Get("X-Line-Mid") != "chat-id" {
			t.Fatalf("request %d MID = %q", index, request.headers.Get("X-Line-Mid"))
		}
		if request.headers.Get("X-Line-Album") != "album-id" {
			t.Fatalf("request %d album = %q", index, request.headers.Get("X-Line-Album"))
		}
		if request.headers.Get("X-Line-Access") != "line-token" {
			t.Fatalf("request %d access token = %q", index, request.headers.Get("X-Line-Access"))
		}
		if request.headers.Get("User-Agent") == "" {
			t.Fatalf("request %d omitted User-Agent", index)
		}
		if request.headers.Get("X-Line-Application") != "" {
			t.Fatalf("request %d unexpectedly sent X-Line-Application", index)
		}
	}
}

func TestDownloadAlbumPreviewClassifiesHTTPStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		check  func(error) bool
	}{
		{
			name:   "encoding incomplete",
			status: http.StatusAccepted,
			check:  func(err error) bool { return errors.Is(err, ErrOBSEncodingIncomplete) },
		},
		{
			name:   "missing object",
			status: http.StatusNotFound,
			check:  func(err error) bool { return errors.Is(err, ErrOBSObjectNotFound) },
		},
		{
			name:   "unauthorized",
			status: http.StatusForbidden,
			check:  IsUnauthorizedStatus,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient("line-token")
			client.channelTokenCache = map[string]cachedChannelAccessToken{
				albumPreviewChannelID: {
					token:     "channel-token",
					expiresAt: time.Now().Add(time.Hour),
				},
			}
			client.OBSClient = &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return obsResponse(test.status, "error"), nil
				}),
			}

			_, err := client.DownloadAlbumPreview(
				context.Background(),
				"preview-oid",
				"chat-id",
				"",
			)
			if !test.check(err) {
				t.Fatalf("err = %v, wrong classification", err)
			}
		})
	}
}

func TestDownloadOBSAllEncryptedMediaUseFullObject(t *testing.T) {
	for _, sid := range []string{"emi", "emv", "ema", "emf"} {
		t.Run(sid, func(t *testing.T) {
			var requests []observedOBSRequest
			client := NewClient("synthetic")
			installCachedOBSToken(client)
			client.OBSClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, observedOBSRequest{path: req.URL.Path, query: req.URL.RawQuery, headers: req.Header.Clone()})
				if strings.HasSuffix(req.URL.Path, "object_info.obs") {
					return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
				}
				return obsResponse(http.StatusOK, "full media"), nil
			})}
			data, err := client.DownloadOBSWithSIDOptions(context.Background(), "object-id", "123", sid, OBSDownloadOptions{OBSPop: "a b&c", MaxBytes: 100})
			if err != nil || string(data) != "full media" || len(requests) != 2 {
				t.Fatal("download failed", err)
			}
			base := "/r/talk/" + sid + "/object-id"
			if requests[0].path != base+"/object_info.obs" || requests[1].path != base {
				t.Fatal("download requested a preview or wrong object", requests)
			}
			for _, request := range requests {
				if request.query != "p=a+b%26c" || request.headers.Get("X-Talk-Meta") == "" || request.headers.Get("X-Line-Access") != "obs-token" || request.headers.Get("X-Line-Application") != lineApplicationHeader {
					t.Fatal("incorrect media headers/query")
				}
			}
		})
	}
}

func TestAcquireChannelAccessTokenContextCancellationDoesNotStall(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := NewClient("line-token")
	client.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			select {
			case <-release:
				return obsResponse(http.StatusOK, `{"code":0,"message":"OK","data":{"channelAccessToken":"channel-token","expiration":9999999999999}}`), nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}),
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := client.AcquireChannelAccessTokenContext(context.Background(), albumPreviewChannelID)
		firstDone <- err
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secondDone := make(chan error, 1)
	go func() {
		_, err := client.AcquireChannelAccessTokenContext(ctx, albumPreviewChannelID)
		secondDone <- err
	}()

	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("canceled caller stalled behind in-flight channel token RPC")
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first caller failed: %v", err)
	}
}

func TestDownloadAlbumPreviewValidatesInputsBeforeAcquiringToken(t *testing.T) {
	cases := []struct {
		name             string
		oid, chat, album string
	}{
		{"empty oid", "", "c1234", ""},
		{"dot oid", ".", "c1234", ""},
		{"dotdot oid", "a..b", "c1234", ""},
		{"slash oid", "a/b", "c1234", ""},
		{"backslash oid", `a\b`, "c1234", ""},
		{"space oid", "a b", "c1234", ""},
		{"control oid", "a\nb", "c1234", ""},
		{"overlong oid", strings.Repeat("a", 257), "c1234", ""},
		{"empty chat", "oid-1", "", ""},
		{"newline chat", "oid-1", "c1234\r\nX-Inject: 1", ""},
		{"space chat", "oid-1", "c 1234", ""},
		{"newline album", "oid-1", "c1234", "alb\n1"},
		{"space album", "oid-1", "c1234", "alb 1"},
		{"overlong album", "oid-1", "c1234", strings.Repeat("b", 257)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokenCalls := 0
			obsCalls := 0
			client := NewClient("line-token")
			client.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				tokenCalls++
				return obsResponse(http.StatusOK, `{"code":0,"data":{"channelAccessToken":"tok"}}`), nil
			})}
			client.OBSClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				obsCalls++
				return obsResponse(http.StatusOK, "img"), nil
			})}
			if _, err := client.DownloadAlbumPreview(context.Background(), tc.oid, tc.chat, tc.album); err == nil {
				t.Fatalf("accepted unsafe inputs: oid=%q chat=%q album=%q", tc.oid, tc.chat, tc.album)
			}
			if tokenCalls != 0 || obsCalls != 0 {
				t.Fatalf("issued token (%d) or OBS (%d) calls on invalid input", tokenCalls, obsCalls)
			}
		})
	}
}

func TestDownloadAlbumPreviewStatusPrecedesErrorBodySizeCap(t *testing.T) {
	largeBody := "private-secret-" + strings.Repeat("x", maxErrorResponseBytes+1024)
	tests := []struct {
		name   string
		status int
		check  func(error) bool
	}{
		{"403 oversized body stays unauthorized", http.StatusForbidden, IsUnauthorizedStatus},
		{"401 oversized body stays unauthorized", http.StatusUnauthorized, IsUnauthorizedStatus},
		{"202 oversized body stays encoding incomplete", http.StatusAccepted, func(err error) bool { return errors.Is(err, ErrOBSEncodingIncomplete) }},
		{"404 oversized body stays not found", http.StatusNotFound, func(err error) bool { return errors.Is(err, ErrOBSObjectNotFound) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient("line-token")
			client.channelTokenCache = map[string]cachedChannelAccessToken{
				albumPreviewChannelID: {token: "channel-token", expiresAt: time.Now().Add(time.Hour)},
			}
			client.OBSClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return obsResponse(tc.status, largeBody), nil
			})}
			_, err := client.DownloadAlbumPreview(context.Background(), "oid-1", "c1234", "alb-1")
			if err == nil || !tc.check(err) {
				t.Fatalf("status %d: err = %v", tc.status, err)
			}
			if errors.Is(err, ErrOBSSizeLimit) || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("status %d misclassified or leaked body: %v", tc.status, err)
			}
		})
	}
}

func TestDownloadAlbumPreviewExact20MiBLimit(t *testing.T) {
	client := NewClient("line-token")
	client.channelTokenCache = map[string]cachedChannelAccessToken{
		albumPreviewChannelID: {token: "channel-token", expiresAt: time.Now().Add(time.Hour)},
	}
	exact := strings.Repeat("a", maxAlbumPreviewBytes)
	client.OBSClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return obsResponse(http.StatusOK, exact), nil
	})}
	data, err := client.DownloadAlbumPreview(context.Background(), "oid-1", "c1234", "")
	if err != nil || len(data) != maxAlbumPreviewBytes {
		t.Fatalf("exact 20 MiB failed: len=%d err=%v", len(data), err)
	}

	client.OBSClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return obsResponse(http.StatusOK, exact+"b"), nil
	})}
	data, err = client.DownloadAlbumPreview(context.Background(), "oid-1", "c1234", "")
	if !errors.Is(err, ErrOBSSizeLimit) || data != nil {
		t.Fatalf("20 MiB + 1: data=%d err=%v, want ErrOBSSizeLimit", len(data), err)
	}
}

func TestDownloadOBSOriginal404FallsBackToDefaultVariant(t *testing.T) {

	var requests []observedOBSRequest
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, observedOBSRequest{path: req.URL.Path, query: req.URL.RawQuery})
			switch req.URL.Path {
			case "/r/talk/m/msg-1/object_info.obs":
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			case "/r/talk/m/msg-1/original":
				return obsResponse(http.StatusNotFound, "original not available"), nil
			case "/r/talk/m/msg-1":
				return obsResponse(http.StatusOK, "default-bytes"), nil
			default:
				return obsResponse(http.StatusInternalServerError, "unexpected"), nil
			}
		}),
	}

	data, err := client.DownloadOBSWithSIDOptions(context.Background(), "msg-1", "", "m", OBSDownloadOptions{
		TID:      "original",
		OBSPop:   "pop-1",
		MaxBytes: 13,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "default-bytes" {
		t.Fatalf("data = %q, want default-bytes", data)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want 3 (preflight + /original + default)", len(requests))
	}
	if requests[1].path != "/r/talk/m/msg-1/original" || requests[2].path != "/r/talk/m/msg-1" || requests[2].query != "p=pop-1" {
		t.Fatalf("unexpected fallback requests: %#v", requests)
	}
}

func TestDownloadOBSOriginalFallbackEnforcesMaxBytes(t *testing.T) {

	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/r/talk/m/msg-1/object_info.obs":
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			case "/r/talk/m/msg-1/original":
				return obsResponse(http.StatusNotFound, "long error body exceeding maxBytes"), nil
			case "/r/talk/m/msg-1":
				return obsResponse(http.StatusOK, "123456"), nil
			default:
				return obsResponse(http.StatusInternalServerError, "unexpected"), nil
			}
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "msg-1", "", "m", OBSDownloadOptions{
		TID:      "original",
		MaxBytes: 5,
	})
	if !errors.Is(err, ErrOBSSizeLimit) {
		t.Fatalf("err = %v, want ErrOBSSizeLimit", err)
	}
}

func TestDownloadOBSOriginalNon404DoesNotFallBack(t *testing.T) {

	var paths []string
	client := NewClient("line-token")
	installCachedOBSToken(client)
	client.OBSClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			paths = append(paths, req.URL.Path)
			if strings.HasSuffix(req.URL.Path, "object_info.obs") {
				return obsResponse(http.StatusOK, `{"status":"exist","encodeStatus":"done"}`), nil
			}
			return obsResponse(http.StatusForbidden, "forbidden"), nil
		}),
	}

	_, err := client.DownloadOBSWithSIDOptions(context.Background(), "msg-1", "", "m", OBSDownloadOptions{
		TID: "original",
	})
	if !IsUnauthorizedStatus(err) {
		t.Fatalf("err = %v, want unauthorized status", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, must not fall back on 403", paths)
	}
}
