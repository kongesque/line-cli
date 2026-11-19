package line

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	thrift "github.com/apache/thrift/lib/go/thrift"
)

func encodeTestSquareReply(t *testing.T, name string, sequence int32, serviceException bool) []byte {
	t.Helper()
	buffer := thrift.NewTMemoryBuffer()
	protocol := thrift.NewTCompactProtocolConf(buffer, nil)
	ctx := context.Background()
	if err := protocol.WriteMessageBegin(ctx, name, thrift.REPLY, sequence); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteStructBegin(ctx, name+"_result"); err != nil {
		t.Fatal(err)
	}
	fieldID := int16(0)
	if serviceException {
		fieldID = 1
	}
	if err := protocol.WriteFieldBegin(ctx, "", thrift.STRUCT, fieldID); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteStructBegin(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if serviceException {
		if err := protocol.WriteFieldBegin(ctx, "", thrift.I32, 1); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteI32(ctx, 42); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteFieldEnd(ctx); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteFieldBegin(ctx, "", thrift.STRING, 2); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteString(ctx, "synthetic Square failure"); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteFieldEnd(ctx); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := protocol.WriteFieldBegin(ctx, "", thrift.STRING, 1); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteString(ctx, "ok"); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteFieldEnd(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := protocol.WriteFieldStop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteStructEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteFieldEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteFieldStop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteStructEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteMessageEnd(ctx); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buffer.Bytes()...)
}

func encodeTestSquareApplicationException(t *testing.T, name string, sequence int32) []byte {
	t.Helper()
	buffer := thrift.NewTMemoryBuffer()
	protocol := thrift.NewTCompactProtocolConf(buffer, nil)
	ctx := context.Background()
	if err := protocol.WriteMessageBegin(ctx, name, thrift.EXCEPTION, sequence); err != nil {
		t.Fatal(err)
	}
	exception := thrift.NewTApplicationException(thrift.INTERNAL_ERROR, "synthetic application failure")
	if err := exception.Write(ctx, protocol); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteMessageEnd(ctx); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buffer.Bytes()...)
}

func fixtureSquareSpec() squareOperationSpec {
	return squareOperationSpec{
		host:   "square.invalid",
		path:   "/api/square/test",
		method: "readFixture",
		validateArguments: func(args []SquareField) error {
			if len(args) != 0 {
				return errors.New("fixture operation takes no arguments")
			}
			return nil
		},
	}
}

func TestEncodeSquareRequest(t *testing.T) {
	request := SquareReadRequest{
		Method:     "getSquareReadFixture",
		SequenceID: 7,
		Arguments: []SquareField{
			{ID: 1, Value: "square-fixture"},
			{ID: 2, Value: int64(123)},
			{ID: 3, Value: true},
		},
	}
	body, err := EncodeSquareRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	buffer := thrift.NewTMemoryBuffer()
	buffer.Write(body)
	protocol := thrift.NewTCompactProtocolConf(buffer, nil)
	ctx := context.Background()
	name, messageType, sequence, err := protocol.ReadMessageBegin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if name != request.Method || messageType != thrift.CALL || sequence != 7 {
		t.Fatalf("message = (%q,%d,%d)", name, messageType, sequence)
	}
	if _, err := protocol.ReadStructBegin(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		id int16
	}{
		{id: 1},
		{id: 2},
		{id: 3},
	} {
		_, typeID, fieldID, err := protocol.ReadFieldBegin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if fieldID != want.id {
			t.Fatalf("field = %d, want %d", fieldID, want.id)
		}
		if err := thrift.SkipDefaultDepth(ctx, protocol, typeID); err != nil {
			t.Fatal(err)
		}
		if err := protocol.ReadFieldEnd(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompactProtocolGoldenBytes(t *testing.T) {
	// Compact Protocol v1 reference bytes, written independently of the Go
	// runtime so a Rust line-rs fixture can be compared byte-for-byte later.
	wantRequest := []byte{0x82, 0x21, 0x01, 0x04, 'p', 'i', 'n', 'g', 0x18, 0x01, 'x', 0x00}
	gotRequest, err := EncodeSquareRequest(SquareReadRequest{
		Method:     "ping",
		SequenceID: 1,
		Arguments:  []SquareField{{ID: 1, Value: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotRequest, wantRequest) {
		t.Fatalf("request bytes = % x, want % x", gotRequest, wantRequest)
	}

	// Reply for ping, seqid 1, result field 0 = "ok". This anchors decoding
	// against the protocol encoding, not a
	// response generated by the same runtime.
	goldenReply := []byte{0x82, 0x41, 0x01, 0x04, 'p', 'i', 'n', 'g', 0x08, 0x00, 0x02, 'o', 'k', 0x00}
	response, err := DecodeSquareResponse(context.Background(), goldenReply)
	if err != nil {
		t.Fatal(err)
	}
	if response.Method != "ping" || response.SequenceID != 1 || len(response.Fields) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Fields[0].ID != 0 || response.Fields[0].Value != "ok" {
		t.Fatalf("golden result = %#v", response.Fields[0])
	}
}

func TestDecodeSquareSuccess(t *testing.T) {
	body := encodeTestSquareReply(t, "getSquareReadFixture", 7, false)
	response, err := DecodeSquareResponse(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Method != "getSquareReadFixture" || response.SequenceID != 7 {
		t.Fatalf("message = (%q,%d)", response.Method, response.SequenceID)
	}
	if len(response.Fields) != 1 {
		t.Fatalf("fields = %d, want 1", len(response.Fields))
	}
	structValue, ok := response.Fields[0].Value.(SquareStruct)
	if !ok {
		t.Fatalf("field value = %T, want SquareStruct", response.Fields[0].Value)
	}
	if len(structValue.Fields) != 1 || structValue.Fields[0].Value != "ok" {
		t.Fatalf("nested fields = %#v", structValue.Fields)
	}
}

func TestDecodeSquareApplicationException(t *testing.T) {
	body := encodeTestSquareApplicationException(t, "getSquareReadFixture", 7)
	_, err := DecodeSquareResponse(context.Background(), body)
	var application *SquareApplicationError
	if !errors.As(err, &application) {
		t.Fatalf("err = %T %v, want *SquareApplicationError", err, err)
	}
	if application.ApplicationException == nil || application.ApplicationException.TypeId() != thrift.INTERNAL_ERROR {
		t.Fatalf("application exception = %#v", application.ApplicationException)
	}
	if IsAuthError(err) || IsLoggedOut(err) {
		t.Fatal("Thrift application exception was inventively mapped to session auth")
	}
}

func TestDecodeSquareUnknownResultStructIsOpaque(t *testing.T) {
	body := encodeTestSquareReply(t, "getSquareReadFixture", 7, true)
	response, err := DecodeSquareResponse(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Fields) != 1 || response.Fields[0].ID != 1 {
		t.Fatalf("fields = %#v", response.Fields)
	}
	nested, ok := response.Fields[0].Value.(SquareStruct)
	if !ok || len(nested.Fields) != 2 || nested.Fields[0].ID != 1 || nested.Fields[1].ID != 2 {
		t.Fatalf("unknown result struct was not preserved opaquely: %#v", response.Fields[0].Value)
	}
	if _, err := EncodeSquareRequest(SquareReadRequest{Method: "arbitrary"}); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeSquareMalformedTruncated(t *testing.T) {
	valid := encodeTestSquareReply(t, "getSquareReadFixture", 7, false)
	for _, body := range [][]byte{{}, {0x82}, valid[:len(valid)/2]} {
		if _, err := DecodeSquareResponse(context.Background(), body); err == nil {
			t.Fatal("malformed Square response accepted")
		}
	}
}

func TestSquareHTTPStatusAndOversizedResponse(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		client := NewClient("synthetic-token")
		client.HTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
			}, nil
		})
		square := NewSquareClient(client)
		_, err := square.callReadWithSpec(context.Background(), fixtureSquareSpec(), nil)
		var response *ResponseError
		if !errors.As(err, &response) || response.Status != http.StatusBadGateway {
			t.Fatalf("err = %T %v", err, err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		client := NewClient("synthetic-token")
		client.HTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{0}, maxRPCResponseBytes+1))),
			}, nil
		})
		square := NewSquareClient(client)
		_, err := square.callReadWithSpec(context.Background(), fixtureSquareSpec(), nil)
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("err = %v, want ErrResponseTooLarge", err)
		}
	})
}

func TestSquareUnknownOperationsCannotSendCredentials(t *testing.T) {
	for _, operation := range []SquareReadOperation{
		"https://attacker.invalid/collect",
		"joinSquare",
		"sendSquareMessage",
	} {
		t.Run(string(operation), func(t *testing.T) {
			client := NewClient("synthetic-square-token")
			dispatched := 0
			client.HTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				dispatched++
				return nil, errors.New("unexpected transport dispatch")
			})
			_, err := NewSquareClient(client).CallRead(context.Background(), operation, nil)
			if !errors.Is(err, ErrSquareOperationNotAllowed) || dispatched != 0 {
				t.Fatalf("err = %v, dispatched = %d", err, dispatched)
			}
		})
	}
}

func TestSquareAllowlistBuildsExactHTTPSEndpoint(t *testing.T) {
	got, err := squareEndpoint("square.invalid", "/api/square/read")
	if err != nil || got != "https://square.invalid/api/square/read" {
		t.Fatalf("endpoint = %q, err = %v", got, err)
	}
	for _, tc := range []struct {
		host string
		path string
	}{
		{"", "/api/square/read"},
		{"square.invalid", "//attacker.invalid/collect"},
		{"square.invalid", "/api/square/read?next=https://attacker.invalid"},
		{"square.invalid", "/api/square/read#fragment"},
		{"square.invalid", "/api/square\\read"},
		{"square.invalid@attacker.invalid", "/api/square/read"},
	} {
		if _, err := squareEndpoint(tc.host, tc.path); err == nil {
			t.Fatalf("accepted invalid allowlist entry host=%q path=%q", tc.host, tc.path)
		}
	}
}

func TestSquareRedirectRejectedWithoutCredentialForwarding(t *testing.T) {
	client := NewClient("synthetic-square-token")
	dispatched := 0
	client.HTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		dispatched++
		if request.URL.Host != "square.invalid" {
			t.Fatalf("unexpected initial host %q", request.URL.Host)
		}
		if request.Header.Get("x-line-access") != "synthetic-square-token" {
			t.Fatal("Square request missing existing session access token")
		}
		response := &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}
		response.Header.Set("Location", "https://redirect.invalid/collect")
		return response, nil
	})
	_, err := NewSquareClient(client).callReadWithSpec(context.Background(), fixtureSquareSpec(), nil)
	if !errors.Is(err, ErrRedirectRejected) || dispatched != 1 {
		t.Fatalf("err = %v, dispatched = %d", err, dispatched)
	}
}

func TestSquareContextCancellationAndTimeout(t *testing.T) {
	client := NewClient("synthetic-token")
	client.HTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	square := NewSquareClient(client)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := square.callReadWithSpec(ctx, fixtureSquareSpec(), nil)
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Square cancellation did not stop")
	}

	timeout, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelTimeout()
	_, err := square.callReadWithSpec(timeout, fixtureSquareSpec(), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err = %v", err)
	}
}

func TestTalkRPCRegressionPathUnchanged(t *testing.T) {
	client := NewClient("synthetic-token")
	client.HTTPClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/talk/thrift/Talk/TalkService/getProfile" {
			t.Fatalf("Talk path = %q", request.URL.Path)
		}
		if request.Header.Get("x-hmac") == "" {
			t.Fatal("Talk RPC lost HMAC")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"data":{"mid":"u-fixture"}}`)),
		}, nil
	})
	profile, err := client.GetProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile.Mid != "u-fixture" {
		t.Fatalf("profile MID = %q", profile.Mid)
	}
}
