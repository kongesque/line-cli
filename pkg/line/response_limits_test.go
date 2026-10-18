package line

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseBoundariesAndClose(t *testing.T) {
	for _, n := range []int{31, 32, 33} {
		for _, advertised := range []int64{-1, 0, 1, 1 << 30} {
			closed := false
			resp := &http.Response{StatusCode: 200, ContentLength: advertised, Body: &policyBody{Reader: strings.NewReader(strings.Repeat("x", n)), closed: &closed}}
			data, err := readResponseBody(resp, 32)
			if !closed || (n <= 32 && (err != nil || len(data) != n)) || (n > 32 && (!errors.Is(err, ErrResponseTooLarge) || data != nil)) {
				t.Fatal("response budget or close failed")
			}
		}
	}
	for _, limit := range []int64{-1, math.MaxInt64} {
		for _, status := range []int{200, 500} {
			closed := false
			_, err := readResponseBody(&http.Response{StatusCode: status, Body: &policyBody{Reader: strings.NewReader(""), closed: &closed}}, limit)
			if !errors.Is(err, ErrInvalidResponseLimit) || !closed {
				t.Fatal("invalid limit accepted")
			}
		}
	}
}

func TestResponseLimitsAfterHTTPDecompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(200)
		w.(http.Flusher).Flush() // chunked framing, no Content-Length
		writer := gzip.NewWriter(w)
		_, _ = io.WriteString(writer, strings.Repeat("x", 4097))
		_ = writer.Close()
	}))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := doLINERequest(server.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Uncompressed {
		t.Fatal("fixture did not exercise HTTP decompression")
	}
	data, err := readResponseBody(resp, 4096)
	if data != nil || !errors.Is(err, ErrResponseTooLarge) {
		t.Fatal("compressed response bypassed budget")
	}
}

func TestOBSPreflightRejectsOversizeBeforeDownload(t *testing.T) {
	c := NewClient("synthetic")
	consumed, closed := 0, false
	c.OBSClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := io.MultiReader(strings.NewReader(`{"status":"exist"}`), io.LimitReader(zeroSpaces{}, 21<<20))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &countedBody{Reader: body, count: &consumed, closed: &closed}}, nil
	})
	err := c.checkOBSObjectReady(context.Background(), OBSBaseURL+"/r/talk/m/synthetic", "synthetic", "123")
	if !errors.Is(err, ErrResponseTooLarge) || !closed || consumed != maxOBSMetadataBytes+1 {
		t.Fatal("unbounded OBS metadata read")
	}
}

type zeroSpaces struct{}

func (zeroSpaces) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

type countedBody struct {
	io.Reader
	count  *int
	closed *bool
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	*b.count += n
	return n, err
}
func (b *countedBody) Close() error { *b.closed = true; return nil }

func TestRPCAndDownloadBudgets(t *testing.T) {
	c := NewClient("synthetic")
	consumed, closed := 0, false
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		consumed, closed = 0, false
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &countedBody{Reader: zeroSpaces{}, count: &consumed, closed: &closed}}, nil
	})
	c.HTTPClient.Transport = transport
	c.OBSClient.Transport = transport
	_, err := c.callRPCContext(context.Background(), "TalkService", "getProfile")
	if !errors.Is(err, ErrResponseTooLarge) || consumed != maxRPCResponseBytes+1 || !closed {
		t.Fatal("unbounded RPC")
	}
	_, err = c.downloadOBSObject(context.Background(), OBSBaseURL+"/r/talk/m/synthetic", "synthetic", "123", 0)
	if !errors.Is(err, ErrOBSSizeLimit) || consumed != maxMediaResponseBytes+1 || !closed {
		t.Fatal("zero download limit was unbounded")
	}
}

func TestSSELineAndAggregateBudgets(t *testing.T) {
	old := sseHTTPClient
	t.Cleanup(func() { sseHTTPClient = old })
	for _, tc := range []struct {
		name, body string
		want       int
		oversize   bool
	}{
		{"exact-line", "data: " + strings.Repeat("x", maxSSELineBytes-7) + "\n\n", 1, false},
		{"exact-event", strings.Repeat("data: "+strings.Repeat("x", maxSSELineBytes-7)+"\n", 4) + "\n", 1, false},
		{"long-event", strings.Repeat("data: "+strings.Repeat("x", maxSSELineBytes-7)+"\n", 4) + "data: x\n\n", 0, true},
		{"long-line", "data: " + strings.Repeat("x", maxSSELineBytes) + "\n\n", 0, true},
		{"unterminated", strings.Repeat("x", maxSSELineBytes+1), 0, true},
		{"multiline", strings.Repeat("data: "+strings.Repeat("x", 1024)+"\n", 4096) + "\n", 0, true},
		{"comments", strings.Repeat(": comment\n", maxSSEEventLines+1) + "\n", 0, true},
		{"names", strings.Repeat("event: name\n", maxSSEEventLines+1) + "\n", 0, true},
		{"exact-count", strings.Repeat("data: x\n", maxSSEEventLines) + "\n", 1, false},
		{"reset", strings.Repeat("data: x\n\n", maxSSEEventLines+1), maxSSEEventLines + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := false
			sseHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: &policyBody{Reader: strings.NewReader(tc.body), closed: &closed}}, nil
			})}
			emitted := 0
			err := NewClient("synthetic").ListenSSE(context.Background(), 0, func(string, string) { emitted++ })
			if errors.Is(err, ErrResponseTooLarge) != tc.oversize || emitted != tc.want || !closed {
				t.Fatal("SSE exceeded budget or emitted partial event", err, emitted)
			}
		})
	}
	// A fresh connection after a failed oversized stream still delivers normally.
	sseHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return auditResponse(req, 200, strings.NewReader("data: recovered\n\n")), nil
	})}
	delivered := ""
	_ = NewClient("synthetic").ListenSSE(context.Background(), 0, func(_, data string) { delivered = data })
	if delivered != "recovered" {
		t.Fatal("reconnect lost valid event")
	}
}

func FuzzBoundedResponse(f *testing.F) {
	f.Add([]byte("synthetic"), uint16(4))
	f.Add([]byte{}, uint16(0))
	f.Fuzz(func(t *testing.T, data []byte, limit uint16) {
		result, err := readBounded(bytes.NewReader(data), int64(limit))
		if len(data) > int(limit) {
			if !errors.Is(err, ErrResponseTooLarge) || result != nil {
				t.Fatal("oversize accepted")
			}
		} else if err != nil || !bytes.Equal(data, result) {
			t.Fatal("valid body changed")
		}
	})
}
func FuzzSSELine(f *testing.F) {
	f.Add("data: synthetic\n")
	f.Add(strings.Repeat("x", 4097))
	f.Fuzz(func(t *testing.T, data string) {
		line, err := readSSELine(bufio.NewReader(strings.NewReader(data)))
		if len(line) > maxSSELineBytes {
			t.Fatal("line exceeded limit")
		}
		n := strings.IndexByte(data, '\n')
		if n < 0 {
			n = len(data)
		} else {
			n++
		}
		if n > maxSSELineBytes && !errors.Is(err, ErrResponseTooLarge) {
			t.Fatal("oversize line accepted")
		}
	})
}

func TestRPCBudgetAcceptsLargeSyntheticHistory(t *testing.T) {
	// 100 messages, each with 10,000 four-byte runes, exceed normal CLI UTF-16
	// text limits but still fit comfortably alongside envelopes and key metadata.
	messages := make([]map[string]string, 100)
	for i := range messages {
		messages[i] = map[string]string{"id": "123", "text": strings.Repeat("😀", 10000)}
	}
	body, _ := json.Marshal(map[string]any{"code": 0, "data": messages})
	c := NewClient("synthetic")
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return auditResponse(req, 200, bytes.NewReader(body)), nil
	})
	got, err := c.callRPCContext(context.Background(), "TalkService", "getRecentMessagesV2")
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("valid history rejected by response budget")
	}
}
func TestErrorBodyBudget(t *testing.T) {
	closed := false
	consumed := 0
	resp := &http.Response{StatusCode: 500, Body: &countedBody{Reader: zeroSpaces{}, count: &consumed, closed: &closed}}
	_, err := readResponseBody(resp, maxRPCResponseBytes)
	if !errors.Is(err, ErrResponseTooLarge) || consumed != maxErrorResponseBytes+1 || !closed {
		t.Fatal("error body budget not enforced")
	}
}
