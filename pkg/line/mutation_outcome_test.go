package line

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMutationResponseRequiresExplicitSuccess(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `garbage`, `{"code":10051,"data":{"secret":"token"}}`} {
		err := mutationResponse([]byte(body))
		if err == nil || strings.Contains(err.Error(), "token") {
			t.Fatalf("unsafe success/error for %s", body)
		}
	}
	if err := mutationResponse([]byte(`{"code":0,"data":null}`)); err != nil {
		t.Fatal(err)
	}
}
func TestAuthoritativeRejectionIsConservative(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{responseError(403, []byte(`secret`)), true},
		{responseError(200, []byte(`{"code":10004}`)), true},
		{responseError(500, []byte(`{"code":10004}`)), false},
		{responseError(400, []byte(`{"code":10051,"data":{"name":"TalkException","code":5}}`)), false},
		{responseError(400, []byte(`{"code":999,"message":"access token refresh required"}`)), false},
		{errors.New("HTTP 403 secret"), false}, {context.Canceled, false}, {io.EOF, false},
	} {
		if IsAuthoritativeRejection(tc.err) != tc.want {
			t.Fatalf("wrong rejection classification: %v", tc.err)
		}
	}
}

func TestUnsendDoesNotConfirmApplicationFailure(t *testing.T) {
	client := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/unsendMessage", `{"code":10051,"message":"secret"}`, nil)
	err := client.UnsendMessageContext(context.Background(), 1, "9007199254740993")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("bad unsend result: %v", err)
	}
}
