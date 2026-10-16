package line

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMediaFlowRequestAndMalformedResponses(t *testing.T) {
	var body string
	c := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/determineMediaMessageFlow", `{"code":0,"data":{"flowMap":{"1":2},"cacheTtlMillis":"60000"}}`, &body)
	flow, err := c.DetermineMediaMessageFlowContext(context.Background(), "u-peer")
	if err != nil || flow.FlowMap["1"] != 2 || body != `[{"chatMid":"u-peer"}]` {
		t.Fatal("bad media flow request", body, err)
	}
	for _, response := range []string{`{}`, `{"code":0}`, `{"code":0,"data":null}`, `{"code":0,"data":{}}`, `{"code":0,"data":{"flowMap":{"1":"2"}}}`, `{"code":4,"message":"private body"}`} {
		c := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/determineMediaMessageFlow", response, nil)
		if _, err := c.DetermineMediaMessageFlowContext(context.Background(), "u-peer"); err == nil || strings.Contains(err.Error(), "private body") {
			t.Fatal("invalid/unsafe media flow response", err)
		}
	}
}

func TestImageUploadTransportContracts(t *testing.T) {
	obsTokenMu.Lock()
	old, expiry := obsTokenCache, obsTokenExpiry
	obsTokenCache, obsTokenExpiry = "synthetic-obs-token", time.Now().Add(time.Hour)
	obsTokenMu.Unlock()
	defer func() { obsTokenMu.Lock(); obsTokenCache, obsTokenExpiry = old, expiry; obsTokenMu.Unlock() }()
	for _, kind := range []string{"encrypted", "preview", "plain"} {
		c := NewClient("synthetic")
		calls := 0
		c.OBSClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "POST" || r.Header.Get("x-line-access") != "synthetic-obs-token" || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatal("incorrect OBS upload headers")
			}
			data, _ := io.ReadAll(r.Body)
			if string(data) != "prepared bytes" {
				t.Fatal("wrong upload body")
			}
			encoded, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Obs-Params"))
			if err != nil {
				t.Fatal(err)
			}
			var params map[string]string
			if json.Unmarshal(encoded, &params) != nil {
				t.Fatal("invalid OBS parameters")
			}
			typ := "file"
			switch kind {
			case "encrypted":
				if !strings.HasPrefix(r.URL.Path, "/r/talk/emi/reqid-") {
					t.Fatal("bad encrypted image URL")
				}
			case "preview":
				if r.URL.Path != "/r/talk/emi/oid__ud-preview" {
					t.Fatal("bad preview URL")
				}
			case "plain":
				typ = "image"
				if r.URL.Path != "/r/talk/m/123" {
					t.Fatal("bad plain image URL")
				}
			}
			if kind == "plain" && !strings.HasSuffix(params["name"], ".jpg") {
				t.Fatal("plain image filename must identify JPEG")
			}
			if params["ver"] != "2.0" || params["type"] != typ || params["name"] == "" {
				t.Fatal("incorrect OBS parameters")
			}
			return &http.Response{StatusCode: 201, Header: http.Header{"X-Obs-Oid": []string{"oid"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		var err error
		switch kind {
		case "encrypted":
			var oid string
			oid, err = c.UploadOBSWithSIDContext(context.Background(), []byte("prepared bytes"), "emi")
			if oid != "oid" {
				t.Fatal("missing oid")
			}
		case "preview":
			err = c.UploadOBSWithOIDAndSIDContext(context.Background(), []byte("prepared bytes"), "oid__ud-preview", "emi")
		case "plain":
			err = c.UploadOBSPlainContext(context.Background(), []byte("prepared bytes"), "123", "image")
		}
		if err != nil || calls != 1 {
			t.Fatal("upload failed/replayed", err, calls)
		}
	}
}

func TestImagePreviewCancellationAndLostResponse(t *testing.T) {
	obsTokenMu.Lock()
	old, expiry := obsTokenCache, obsTokenExpiry
	obsTokenCache, obsTokenExpiry = "synthetic", time.Now().Add(time.Hour)
	obsTokenMu.Unlock()
	defer func() { obsTokenMu.Lock(); obsTokenCache, obsTokenExpiry = old, expiry; obsTokenMu.Unlock() }()
	for _, mode := range []string{"before", "during", "lost"} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		c := NewClient("synthetic")
		c.OBSClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if mode == "during" {
				cancel()
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			return nil, io.ErrUnexpectedEOF
		})}
		if mode == "before" {
			cancel()
		}
		err := c.UploadOBSWithOIDAndSIDContext(ctx, nil, "oid__ud-preview", "emi")
		cancel()
		if err == nil || mode != "lost" && !errors.Is(err, context.Canceled) || mode == "before" && calls != 0 || calls > 1 {
			t.Fatal("preview cancellation/retry contract", err, calls)
		}
	}
}
