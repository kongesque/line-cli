package messaging

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type claimGroupAPI struct {
	*fakeAPI
	wire *line.Client
}

func (a *claimGroupAPI) GetLastE2EEGroupSharedKeyContext(ctx context.Context, chat string) (*line.E2EEGroupSharedKey, error) {
	return a.wire.GetLastE2EEGroupSharedKeyContext(ctx, chat)
}

func TestPlaintextConsentCapabilityAndFailureBoundaries(t *testing.T) {
	for _, allow := range []bool{false, true} {
		for _, tc := range []struct {
			name, body     string
			network, sends bool
		}{
			{name: "exceed_max_member", sends: true, body: `{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":100,"reason":"exceed max member"}}`},
			{name: "member_settings_off", sends: true, body: `{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":98,"reason":"member settings off"}}`},
			{name: "missing_key", body: `{"code":0,"data":{}}`},
			{name: "malformed_key", body: `{"code":0,"data":{"groupKeyId":33,"creatorKeyId":11,"receiverKeyId":11,"encryptedSharedKey":""}}`},
			{name: "network_failure", network: true},
		} {
			t.Run(fmt.Sprintf("%s/allow=%t", tc.name, allow), func(t *testing.T) {
				c, f, _, store := setup(t, false)
				wire := line.NewClient("synthetic-token")
				wire.HTTPClient.Transport = claimTransport(func(r *http.Request) (*http.Response, error) {
					if tc.network {
						return nil, errors.New("synthetic network failure")
					}
					status := 200
					if tc.sends {
						status = 400
					}
					return claimResponse(r, status, tc.body), nil
				})
				api := &claimGroupAPI{fakeAPI: f, wire: wire}
				c.Session.NewClient = func(string) session.API { return api }
				before := store.state.LastReqSeq
				result, err := c.SendMentionsWithOptionsContext(context.Background(), "c-group", "synthetic private text", "", nil, SendOptions{AllowPlaintext: allow})
				if tc.sends && allow {
					if err != nil || result == nil || result.Encrypted || f.sends != 1 || f.sent.Text != "synthetic private text" || len(f.sent.Chunks) != 0 || store.state.LastReqSeq <= before {
						t.Fatal("expected one persisted-sequence plaintext send after explicit unavailability")
					}
					t.Log("CONFIRMED: explicit capability rejection causes one plaintext send and Encrypted=false")
				} else if err == nil || f.sends != 0 || f.registrations != 0 || store.state.LastReqSeq != before {
					t.Fatal("missing/malformed/network failure incorrectly allowed mutation")
				} else {
					t.Log("CONFIRMED: failure stops before any send, group registration, or sequence reservation")
				}
			})
		}
	}

}

func TestSendConsentAcrossContentAndDestinations(t *testing.T) {
	img := imageFixture(t)
	for _, chat := range []string{"u-peer", "c-group", "r-room"} {
		for _, mode := range []string{"account-text", "account-reply", "account-file", "account-image", "image-flow", "peer-off", "group-off"} {
			for _, allow := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", chat, mode, allow), func(t *testing.T) {
					c, f, crypt, store := setup(t, strings.HasPrefix(mode, "account"))
					api := &imageAPI{fakeAPI: f, flow: 1}
					c.crypto = &fileCrypto{fakeCrypto: crypt}
					c.Session.NewClient = func(string) session.API { return api }
					f.history = []*line.Message{{ID: "123", From: "u-self", To: chat, ToType: toType(chat)}}
					f.negotiateErr = line.ErrE2EEDisabled
					f.groupErr = line.ErrE2EEDisabled
					opts := SendOptions{AllowPlaintext: allow}
					var result *SendResult
					var err error
					switch mode {
					case "account-file":
						files := &fileAPI{fakeAPI: f}
						c.Session.NewClient = func(string) session.API { return files }
						result, err = c.SendFileWithOptionsContext(context.Background(), chat, Attachment{Name: "test.txt", Data: []byte("synthetic")}, "123", opts)
					case "account-image", "image-flow":
						result, err = c.SendImageWithOptionsContext(context.Background(), chat, *img, "", opts)
					default:
						result, err = c.SendMentionsWithOptionsContext(context.Background(), chat, "@Peer", "123", []Mention{{Start: 0, End: 5, MID: mentionPeer}}, opts)
					}
					if allow {
						if err != nil || result == nil || result.Encrypted || f.sends != 1 {
							t.Fatal("explicit plaintext consent not honored", err)
						}
					} else {
						var safe *fault.Error
						if !errors.As(err, &safe) || safe.Code != "encryption_required" || f.sends != 0 || f.registrations != 0 || len(api.order) != 0 || store.state.LastReqSeq != 0 {
							t.Fatal("plaintext rejection had side effects or wrong error", err)
						}
					}
				})
			}
		}
	}
}

func TestLegacySendMethodsRequireEncryption(t *testing.T) {
	img := imageFixture(t)
	c, f, _, store := setup(t, true)
	f.history = []*line.Message{{ID: "123", From: "u-self", To: "u-peer"}}
	calls := []func() (*SendResult, error){
		func() (*SendResult, error) { return c.Send("u-peer", "text") },
		func() (*SendResult, error) { return c.SendReply("u-peer", "text", "123") },
		func() (*SendResult, error) { return c.SendFile("u-peer", Attachment{Name: "test.txt"}, "") },
		func() (*SendResult, error) {
			return c.SendMentionsContext(context.Background(), "u-peer", "text", "", nil)
		},
		func() (*SendResult, error) { return c.SendImageContext(context.Background(), "u-peer", *img, "") },
	}
	for _, call := range calls {
		_, err := call()
		var safe *fault.Error
		if !errors.As(err, &safe) || safe.Code != "encryption_required" || f.sends != 0 || store.state.LastReqSeq != 0 {
			t.Fatal("legacy method permits plaintext", err)
		}
	}
}

type fileFlowAPI struct {
	*fileAPI
	flow *line.MediaMessageFlowResponse
	err  error
}

func (a *fileFlowAPI) DetermineMediaMessageFlowContext(context.Context, string) (*line.MediaMessageFlowResponse, error) {
	return a.flow, a.err
}

func TestFileMediaCapabilityBeforeMutation(t *testing.T) {
	for _, chat := range []string{"u-peer", "c-group", "r-room"} {
		for _, allow := range []bool{false, true} {
			for _, tc := range []struct {
				name  string
				flow  *line.MediaMessageFlowResponse
				err   error
				plain bool
			}{
				{name: "plaintext-only", flow: &line.MediaMessageFlowResponse{FlowMap: map[string]int{"1": 2, "14": 1}}, plain: true},
				{name: "missing-response"},
				{name: "missing-file-type", flow: &line.MediaMessageFlowResponse{FlowMap: map[string]int{"1": 2}}},
				{name: "unknown-file-flow", flow: &line.MediaMessageFlowResponse{FlowMap: map[string]int{"14": 3}}},
				{name: "transport-failure", err: errors.New("synthetic unavailable")},
			} {
				t.Run(fmt.Sprintf("%s/%s/%t", chat, tc.name, allow), func(t *testing.T) {
					c, f, crypt, store := setup(t, false)
					files := &fileAPI{fakeAPI: f}
					api := &fileFlowAPI{fileAPI: files, flow: tc.flow, err: tc.err}
					c.crypto = &fileCrypto{fakeCrypto: crypt}
					c.Session.NewClient = func(string) session.API { return api }
					file := Attachment{Name: "synthetic.txt", Data: []byte("synthetic private file")}
					result, err := c.SendFileWithOptionsContext(context.Background(), chat, file, "", SendOptions{AllowPlaintext: allow})
					if tc.plain && allow {
						if err != nil || result == nil || result.Encrypted || f.sends != 1 || strings.Join(files.order, ",") != "send,plain_upload" {
							t.Fatal("explicit plaintext file policy failed", err)
						}
					} else {
						if err == nil || result != nil || f.sends != 0 || f.registrations != 0 || len(files.order) != 0 || store.state.LastReqSeq != 0 {
							t.Fatal("file capability failure had mutation side effects")
						}
						if tc.plain {
							var safe *fault.Error
							if !errors.As(err, &safe) || safe.Code != "encryption_required" {
								t.Fatal("wrong default file refusal")
							}
						}
					}
				})
			}
		}
	}
}
