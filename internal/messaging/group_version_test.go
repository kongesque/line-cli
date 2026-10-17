package messaging

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestGroupSendHonorsNegotiatedVersion(t *testing.T) {
	for _, version := range []int{0, 1, 2} {
		for _, mode := range []string{"text", "mention", "reply", "file"} {
			t.Run(strconv.Itoa(version)+"/"+mode, func(t *testing.T) {
				c, api, crypto, store := setup(t, false)
				api.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped", SpecVersion: version}
				api.history = []*line.Message{{ID: "123", From: "u-self", To: "c-group", ToType: 2}}
				var result *SendResult
				var err error
				switch mode {
				case "text":
					result, err = c.Send("c-group", "synthetic")
				case "mention":
					result, err = c.SendMentionsContext(context.Background(), "c-group", "@Peer", "", []Mention{{Start: 0, End: 5, MID: mentionPeer}})
				case "reply":
					result, err = c.SendReply("c-group", "synthetic", "123")
				case "file":
					c.crypto = &fileCrypto{fakeCrypto: crypto}
					files := &fileAPI{fakeAPI: api}
					c.Session.NewClient = func(string) session.API { return files }
					result, err = c.SendFile("c-group", Attachment{Name: "synthetic.txt", Data: []byte("synthetic")}, "")
				}
				want := version
				if want == 0 {
					want = 2
				}
				if err != nil || result == nil || !result.Encrypted || api.sends != 1 || api.registrations != 0 || crypto.groupVersion != want || api.sent.ContentMetadata["e2eeVersion"] != strconv.Itoa(want) || api.sent.Text != "" || len(api.sent.Chunks) != 5 || api.sequence != store.state.LastReqSeq {
					t.Fatalf("group send did not preserve negotiation/encryption: version=%d, err=%v", crypto.groupVersion, err)
				}
				if mode == "mention" && api.sent.ContentMetadata["MENTION"] == "" {
					t.Fatal("lost mention metadata")
				}
				if mode == "reply" && api.sent.RelatedMessageID != "123" {
					t.Fatal("lost reply relation")
				}
			})
		}
	}
}

func TestGroupSendRejectsUnsupportedVersionBeforeMutation(t *testing.T) {
	for _, version := range []int{-1, 3, 99} {
		c, api, crypto, store := setup(t, false)
		api.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped", SpecVersion: version}
		before := store.state.LastReqSeq
		if _, err := c.Send("c-group", "synthetic"); err == nil {
			t.Fatal("accepted unsupported version")
		}
		if api.sends != 0 || api.registrations != 0 || len(crypto.unwrapped) != 0 || store.state.LastReqSeq != before {
			t.Fatal("mutated before validating negotiation")
		}
	}
}

func TestGroupVersionRefreshAndEncryptionFailureNeverDowngrade(t *testing.T) {
	c, api, crypto, _ := setup(t, false)
	api.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped", SpecVersion: 1}
	if _, err := c.Send("c-group", "synthetic"); err != nil {
		t.Fatal(err)
	}
	api.group.SpecVersion = 2
	if _, err := c.Send("c-group", "synthetic"); err != nil {
		t.Fatal(err)
	}
	if crypto.groupVersion != 2 || api.sent.ContentMetadata["e2eeVersion"] != "2" {
		t.Fatal("cached stale send version")
	}
	crypto.encryptErr = errors.New("synthetic cipher failure")
	if _, err := c.Send("c-group", "synthetic"); err == nil || api.sends != 2 {
		t.Fatal("encryption failure caused send or plaintext downgrade")
	}
}
