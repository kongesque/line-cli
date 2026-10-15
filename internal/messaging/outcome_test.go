package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func mutationDetails(t *testing.T, err error) *MutationError {
	t.Helper()
	var detail *MutationError
	if !errors.As(err, &detail) {
		t.Fatalf("missing outcome: %v", err)
	}
	return detail
}

func TestSendOutcomeAndSequence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		outcome fault.Outcome
	}{
		{"lost response", io.ErrUnexpectedEOF, fault.Unknown},
		{"rejected", &line.ResponseError{Status: 403}, fault.Rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, api, _, store := setup(t, true)
			api.sendErr = tc.failure
			_, err := c.Send("u-peer", "synthetic")
			e := mutationDetails(t, err)
			if e.Mutation.Outcome != tc.outcome || !e.Mutation.Attempted || e.Mutation.ChatID != "u-peer" || e.Mutation.RequestSequence == nil || *e.Mutation.RequestSequence != store.state.LastReqSeq || api.sends != 1 {
				t.Fatalf("bad outcome: %+v", e.Mutation)
			}
			if !errors.Is(err, tc.failure) {
				t.Fatal("lost cause")
			}
		})
	}
}

func TestSequenceFailureNeverAttemptsSend(t *testing.T) {
	c, api, _, store := setup(t, true)
	store.saveErr = session.ErrDurabilityUncertain
	_, err := c.Send("u-peer", "synthetic")
	e := mutationDetails(t, err)
	if e.Stage != "persist_sequence" || e.Mutation.Outcome != fault.NotAttempted || e.Mutation.Attempted || api.sends != 0 || session.StorageExitCode(err) != 74 {
		t.Fatalf("bad outcome: %+v", e)
	}
}

func TestPlainFileRetainsConfirmedMessageAndFailedUpload(t *testing.T) {
	c, api, _, _ := setup(t, true)
	f := &fileAPI{fakeAPI: api, plainErr: errors.New("secret-token raw-body ciphertext")}
	c.Session.NewClient = func(string) session.API { return f }
	_, err := c.SendFile("u-peer", Attachment{Name: "file.txt"}, "")
	e := mutationDetails(t, err)
	op := e.Mutation
	if op.Outcome != fault.Confirmed || op.MessageID != "server-id" || len(op.Steps) != 2 || op.Steps[1].Outcome != fault.Unknown || e.Stage != "attachment_upload" {
		t.Fatalf("lost receipt: %+v", e)
	}
	encoded, _ := json.Marshal(op)
	if strings.Contains(string(encoded), "secret") || strings.Contains(err.Error(), "raw-body") {
		t.Fatal("leaked protocol data")
	}
}

func TestSuccessfulPreparationSurvivesLaterFailure(t *testing.T) {
	c, api, crypto, _ := setup(t, false)
	f := &fileAPI{fakeAPI: api}
	c.Session.NewClient = func(string) session.API { return f }
	crypto.encryptErr = errors.New("secret crypto material")
	c.crypto = &fileCrypto{fakeCrypto: crypto}
	_, err := c.SendFile("u-peer", Attachment{Name: "file.txt"}, "")
	e := mutationDetails(t, err)
	if e.Mutation.Outcome != fault.NotAttempted || len(e.Mutation.Steps) != 1 || e.Mutation.Steps[0].Outcome != fault.Confirmed || api.sends != 0 {
		t.Fatalf("lost upload: %+v", e)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("leaked crypto cause")
	}
}

func TestCancelledUploadRetainsSeparateOutcomes(t *testing.T) {
	c, api, _, _ := setup(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Session.NewClient = func(string) session.API { return &cancellationAPI{fakeAPI: api, cancel: cancel, phase: "confirmed"} }
	_, err := c.SendFileContext(ctx, "u-peer", Attachment{Name: "file.txt"}, "")
	e := mutationDetails(t, err)
	if e.Mutation.Outcome != fault.Confirmed || len(e.Mutation.Steps) != 2 || e.Mutation.Steps[1].Outcome != fault.NotAttempted || e.Mutation.Steps[1].Attempted {
		t.Fatalf("bad upload outcome: %+v", e.Mutation)
	}
}

type missingReceiptAPI struct{ *fakeAPI }

func (f missingReceiptAPI) SendMessageContext(_ context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	f.fakeAPI.SendMessage(seq, msg)
	return nil, nil
}
func TestMissingSendReceiptIsUnknown(t *testing.T) {
	c, api, _, _ := setup(t, true)
	c.Session.NewClient = func(string) session.API { return missingReceiptAPI{api} }
	_, err := c.Send("u-peer", "synthetic")
	e := mutationDetails(t, err)
	if e.Mutation.Outcome != fault.Unknown || e.Mutation.Steps[0].Outcome != fault.Unknown || api.sends != 1 {
		t.Fatalf("false confirmation: %+v", e)
	}
}
func TestActionOutcomesRetainLargeMessageIDs(t *testing.T) {
	for _, action := range []string{"react", "remove_reaction", "unsend"} {
		c, api, _, store := setup(t, true)
		const id = "9007199254740993"
		api.history = []*line.Message{{ID: id, From: "u-self", To: "u-peer"}}
		f := &actionAPI{fakeAPI: api, err: io.EOF}
		c.Session.NewClient = func(string) session.API { return f }
		var err error
		if action == "unsend" {
			_, err = c.Unsend("u-peer", id)
		} else {
			reaction := "like"
			if action == "remove_reaction" {
				reaction = ""
			}
			_, err = c.React("u-peer", id, reaction, action == "remove_reaction")
		}
		e := mutationDetails(t, err)
		if e.Mutation.Action != action || e.Mutation.MessageID != id || e.Mutation.Outcome != fault.Unknown || *e.Mutation.RequestSequence != store.state.LastReqSeq || f.calls != 1 {
			t.Fatalf("bad action outcome: %+v", e)
		}
	}
}
