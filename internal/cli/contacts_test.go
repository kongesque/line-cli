package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type lookupAPI struct {
	session.API
	contacts      map[string]line.Contact
	buddies       map[string]line.BuddyProfile
	buddyErr      error
	batches       [][]string
	listCalls     int
	response      func([]string) (*line.ContactsResponse, error)
	byUserid      map[string]line.Contact
	useridLookups []string
	useridErr     error
	byPhone       map[string]line.Contact
	phoneLookups  []string
	phoneErr      error
	blockedIDs    []string
	blockedRecIDs []string
	blockedErr    error
	blockedRecErr error
	blockCalls    []blockCall
	blockErr      error
}

type blockCall struct {
	seq     int64
	mid     string
	blocked bool
}

func (f *lookupAPI) GetAllContactIds() ([]string, error) {
	f.listCalls++
	return nil, errors.New("direct lookup must not enumerate friends")
}

func (f *lookupAPI) GetContactsV2(ids []string) (*line.ContactsResponse, error) {
	f.batches = append(f.batches, append([]string(nil), ids...))
	if f.response != nil {
		return f.response(ids)
	}
	response := &line.ContactsResponse{Contacts: make(map[string]line.ContactWrapper)}
	for _, mid := range ids {
		if contact, ok := f.contacts[mid]; ok {
			response.Contacts[mid] = line.ContactWrapper{Contact: contact}
		}
	}
	return response, nil
}

func lookupMID(i int) string { return fmt.Sprintf("u%032x", i) }

func TestContactLookupJSONPreservesOrderAndNames(t *testing.T) {
	first, second := lookupMID(2), lookupMID(1)
	f := &lookupAPI{contacts: map[string]line.Contact{
		first:  {DisplayName: "Original", DisplayNameOverridden: "Custom", StatusMessage: "Hello", PicturePath: "picture"},
		second: {Mid: second, DisplayName: "Alice"},
	}}
	a, out, diagnostics := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"contacts", "--mid", first, "--json", "--mid=" + second, "--mid", first}); err != nil {
		t.Fatal(err)
	}
	wantBatches := [][]string{{first, second}}
	if f.listCalls != 0 || !reflect.DeepEqual(f.batches, wantBatches) {
		t.Fatalf("unexpected requests: list=%d batches=%v", f.listCalls, f.batches)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{
		{"mid": first, "displayName": "Original", "displayNameOverridden": "Custom", "statusMessage": "Hello", "picturePath": "picture", "effectiveDisplayName": "Custom", "status": "resolved"},
		{"mid": second, "displayName": "Alice", "displayNameOverridden": "", "statusMessage": "", "picturePath": "", "effectiveDisplayName": "Alice", "status": "resolved"},
	}
	if !reflect.DeepEqual(rows, want) || diagnostics.Len() != 0 {
		t.Fatalf("unexpected output: %s, diagnostics: %s", out.String(), diagnostics.String())
	}
}

func TestContactLookupSupportedMIDFormatsPreserveCase(t *testing.T) {
	mids := []string{lookupMID(1), "u" + strings.Repeat("A", 41) + "_-", "U" + strings.Repeat("b", 43), "U" + strings.Repeat("B", 43)}
	f := &lookupAPI{contacts: make(map[string]line.Contact)}
	args := []string{"contacts", "--json"}
	for _, mid := range mids {
		args = append(args, "--mid", mid)
		f.contacts[mid] = line.Contact{DisplayName: "Person"}
	}
	a, _, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run(args); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.batches, [][]string{mids}) {
		t.Fatalf("MID case changed: %v", f.batches)
	}
}

func TestContactLookupBatchesWithoutTruncation(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			f := &lookupAPI{contacts: make(map[string]line.Contact)}
			args := []string{"contacts", fmt.Sprintf("--json=%t", jsonOutput)}
			var mids []string
			for i := 200; i >= 0; i-- {
				mid := lookupMID(i)
				mids = append(mids, mid)
				args = append(args, "--mid", mid)
				f.contacts[mid] = line.Contact{DisplayName: "Person"}
			}
			a, out, _ := testApp(nil)
			a.Manager.NewClient = func(string) session.API { return f }
			if err := a.Run(args); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.batches, [][]string{mids[:100], mids[100:200], mids[200:]}) {
				t.Fatalf("unexpected batches: %v", f.batches)
			}
			if jsonOutput {
				var rows []contactLookupResult
				if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != len(mids) {
					t.Fatalf("truncated or invalid results: %v", err)
				}
				for i, row := range rows {
					if row.Mid != mids[i] {
						t.Fatal("input order was lost")
					}
				}
			} else if strings.Count(out.String(), "  Person\n") != 201 || !strings.Contains(out.String(), "Resolved 201 of 201 contacts.") {
				t.Fatal("human lookup was truncated")
			}
		})
	}
}

func TestContactLookupRejectsArgumentsBeforeSessionAccess(t *testing.T) {
	mid := lookupMID(1)
	cases := [][]string{
		{"--mid"}, {"--mid", ""}, {"--mid", "Alice"}, {"--mid", "u123"},
		{"--mid", "c" + mid[1:]}, {"--mid", "r" + strings.Repeat("a", 43)},
		{"--mid", "u" + strings.Repeat("g", 32)}, {"--mid", "U" + mid[1:]},
		{"--mid", mid + "\n"}, {"--mid", " " + mid}, {"--mid", "id:" + mid},
		{"--mid", "u" + strings.Repeat("a", 44)}, {"--mid", "u" + strings.Repeat("/", 43)},
		{"--mid", mid, "--mid", "bad"}, {"--mid", mid, "--search", "Alice"},
		{"--mid", mid, "--search="}, {"--mid", mid, "--limit", "0"},
		{"--mid", mid, "--limit", "20"}, {"--mid", mid, "--limit", "-1"},
		{"--mid", mid, lookupMID(2)}, {mid}, {"--limit", "-1"}, {"--unknown"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, out, _ := testApp(nil)
			a.Lock = func() (func(), error) {
				t.Fatal("opened session before argument validation")
				return nil, nil
			}
			if err := a.Run(append([]string{"contacts"}, args...)); err == nil || out.Len() != 0 {
				t.Fatalf("invalid arguments accepted: %v", err)
			}
		})
	}
}

func TestContactLookupHelpDoesNotOpenSession(t *testing.T) {
	a, out, diagnostics := testApp(nil)
	a.Lock = func() (func(), error) { t.Fatal("help opened session"); return nil, nil }
	if err := a.Run([]string{"contacts", "--help"}); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Len() != 0 || !strings.Contains(out.String(), "--mid MID") || !strings.Contains(out.String(), "aren't friends") {
		t.Fatal("lookup help missing from stdout")
	}
}

func TestContactLookupUnavailableAndBlankNames(t *testing.T) {
	for _, allMissing := range []bool{false, true} {
		t.Run(fmt.Sprintf("allMissing=%t", allMissing), func(t *testing.T) {
			first, missing, blank := lookupMID(1), lookupMID(2), lookupMID(3)
			f := &lookupAPI{contacts: make(map[string]line.Contact)}
			if !allMissing {
				f.contacts[first] = line.Contact{DisplayName: "Alice"}
				f.contacts[blank] = line.Contact{Mid: blank}
			}
			a, out, _ := testApp(nil)
			a.Manager.NewClient = func(string) session.API { return f }
			err := a.Run([]string{"contacts", "--mid", first, "--mid", missing, "--mid", blank, "--json"})
			count := 1
			if allMissing {
				count = 3
			}
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d of 3 requested contacts unavailable", count)) {
				t.Fatalf("missing partial-result diagnostic: %v", err)
			}
			var rows []contactLookupResult
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 3 {
				t.Fatalf("missing complete JSON: %s, %v", out.String(), err)
			}
			if rows[1].Mid != missing || rows[1].Status != "unavailable" || rows[1].EffectiveName != "" || rows[1].DisplayName != "" {
				t.Fatal("unavailable profile was omitted or given a fabricated name")
			}
			if !allMissing && (rows[2].Status != "resolved" || rows[2].EffectiveName != "") {
				t.Fatal("blank name was confused with a missing profile")
			}
		})
	}
}

func TestContactLookupHumanOutputAlwaysIdentifiesEveryRow(t *testing.T) {
	first, blank, missing := lookupMID(1), lookupMID(2), lookupMID(3)
	f := &lookupAPI{contacts: map[string]line.Contact{
		first: {DisplayName: "Original", DisplayNameOverridden: "Name\x1b[2J\n注入"},
		blank: {Mid: blank},
	}}
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"contacts", "--mid", first, "--mid", blank, "--mid", missing}); err == nil {
		t.Fatal("missing contact reported as success")
	}
	for _, text := range []string{first + "  Name", blank + "  (name unavailable)", missing + "  (unavailable)", "Resolved 2 of 3 contacts."} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %q in human output: %s", text, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "Original") || strings.Count(out.String(), "\n") != 6 {
		t.Fatalf("unsafe or incorrect human output: %q", out.String())
	}
}

func TestContactLookupRejectsInconsistentResponsesWithoutOutput(t *testing.T) {
	mid := lookupMID(1)
	cases := map[string]*line.ContactsResponse{
		"nil response": nil,
		"nil map":      {},
		"wrong nested MID": {Contacts: map[string]line.ContactWrapper{
			mid: {Contact: line.Contact{Mid: lookupMID(2), DisplayName: "private-name"}},
		}},
		"unsolicited contact": {Contacts: map[string]line.ContactWrapper{
			lookupMID(2): {Contact: line.Contact{DisplayName: "private-name"}},
		}},
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			f := &lookupAPI{response: func([]string) (*line.ContactsResponse, error) { return response, nil }}
			a, out, _ := testApp(nil)
			a.Manager.NewClient = func(string) session.API { return f }
			err := a.Run([]string{"contacts", "--mid", mid, "--json"})
			if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private-name") || strings.Contains(err.Error(), lookupMID(2)) {
				t.Fatalf("inconsistent response was published: %s, %v", out.String(), err)
			}
		})
	}
}

func TestContactLookupLaterBatchFailurePublishesNothing(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			f := &lookupAPI{}
			failure := errors.New("synthetic transport failure")
			f.response = func(ids []string) (*line.ContactsResponse, error) {
				if len(f.batches) == 2 {
					return nil, failure
				}
				response := &line.ContactsResponse{Contacts: make(map[string]line.ContactWrapper)}
				for _, mid := range ids {
					response.Contacts[mid] = line.ContactWrapper{Contact: line.Contact{DisplayName: "Person"}}
				}
				return response, nil
			}
			a, out, _ := testApp(nil)
			a.Manager.NewClient = func(string) session.API { return f }
			args := []string{"contacts", fmt.Sprintf("--json=%t", jsonOutput)}
			for i := 0; i < 101; i++ {
				args = append(args, "--mid", lookupMID(i))
			}
			if err := a.Run(args); err == nil || out.Len() != 0 || len(f.batches) != 2 {
				t.Fatalf("failed batch retried or published partial output: %s, %v", out.String(), err)
			}
		})
	}
}

func TestContactLookupRejectedSessionHasSafeErrorAndNoOutput(t *testing.T) {
	f := &lookupAPI{response: func([]string) (*line.ContactsResponse, error) {
		return nil, errors.New(`API error 401: {"code":10004,"message":"REQUEST_NEED_LOGIN","private":"synthetic-secret"}`)
	}}
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	err := a.Run([]string{"contacts", "--mid", lookupMID(1), "--json"})
	if err == nil || !strings.Contains(err.Error(), "line login") || strings.Contains(err.Error(), "synthetic-secret") || out.Len() != 0 {
		t.Fatalf("unsafe authentication failure: %s, %v", out.String(), err)
	}
	if len(f.batches) != 1 || f.listCalls != 0 {
		t.Fatal("logged-out lookup was retried or enumerated friends")
	}
}

func TestContactListingJSONSchemaUnchanged(t *testing.T) {
	a, out, _ := testApp(&readAPI{ids: []string{lookupMID(1)}})
	if err := a.Run([]string{"contacts", "--json"}); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0]) != 5 || rows[0]["status"] != "" || rows[0]["effectiveDisplayName"] != "" {
		t.Fatalf("listing schema changed: %s", out.String())
	}
}

func (f *lookupAPI) GetAllContactIdsContext(_ context.Context) ([]string, error) {
	return f.GetAllContactIds()
}

func (f *lookupAPI) GetContactsV2Context(_ context.Context, ids []string) (*line.ContactsResponse, error) {
	return f.GetContactsV2(ids)
}

func (f *lookupAPI) GetBuddyProfileContext(_ context.Context, mid string) (*line.BuddyProfile, error) {
	if f.buddyErr != nil {
		return nil, f.buddyErr
	}
	if b, ok := f.buddies[mid]; ok {
		return &b, nil
	}
	return nil, line.ErrBuddyNotFound
}

func TestContactLookupFallsBackToBuddyProfile(t *testing.T) {
	official, unknown := lookupMID(10), lookupMID(11)
	f := &lookupAPI{
		contacts: map[string]line.Contact{},
		buddies: map[string]line.BuddyProfile{
			official: {Mid: official, DisplayName: "LINE Official", PicturePath: "/buddy"},
		},
	}
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"contacts", "--mid", official, "--json"}); err != nil {
		t.Fatal(err)
	}
	var rows []contactLookupResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("unexpected buddy JSON: %s %v", out.String(), err)
	}
	if rows[0].Status != "resolved" || rows[0].EffectiveName != "LINE Official" || rows[0].PicturePath != "/buddy" {
		t.Fatalf("unexpected buddy row: %+v", rows[0])
	}

	// Transient BuddyService errors propagate without partial output.
	out.Reset()
	f.buddyErr = errors.New("network unavailable")
	if err := a.Run([]string{"contacts", "--mid", unknown, "--json"}); err == nil || out.Len() != 0 {
		t.Fatalf("transient buddy error swallowed or leaked partial output: err=%v out=%q", err, out.String())
	}
}

func (f *lookupAPI) FindContactByUseridContext(_ context.Context, userid string) (*line.Contact, error) {
	f.useridLookups = append(f.useridLookups, userid)
	if f.useridErr != nil {
		return nil, f.useridErr
	}
	if c, ok := f.byUserid[userid]; ok {
		return &c, nil
	}
	return nil, line.ErrContactNotFound
}

func (f *lookupAPI) FindContactByPhoneContext(_ context.Context, phone string) (*line.Contact, error) {
	f.phoneLookups = append(f.phoneLookups, phone)
	if f.phoneErr != nil {
		return nil, f.phoneErr
	}
	if c, ok := f.byPhone[phone]; ok {
		return &c, nil
	}
	return nil, line.ErrContactNotFound
}

func (f *lookupAPI) GetBlockedContactIdsContext(context.Context) ([]string, error) {
	if f.blockedErr != nil {
		return nil, f.blockedErr
	}
	return f.blockedIDs, nil
}

func (f *lookupAPI) GetBlockedRecommendationIdsContext(context.Context) ([]string, error) {
	if f.blockedRecErr != nil {
		return nil, f.blockedRecErr
	}
	return f.blockedRecIDs, nil
}

func (f *lookupAPI) SetContactBlockedContext(_ context.Context, seq int64, mid string, blocked bool) error {
	f.blockCalls = append(f.blockCalls, blockCall{seq: seq, mid: mid, blocked: blocked})
	return f.blockErr
}

func TestContactDiscoveryUseridAndPhone(t *testing.T) {
	aliceMID, bobMID := lookupMID(20), lookupMID(21)
	f := &lookupAPI{
		byUserid: map[string]line.Contact{
			"alice_123": {Mid: aliceMID, DisplayName: "Alice"},
		},
		byPhone: map[string]line.Contact{
			"+81 9012345678": {Mid: bobMID, DisplayName: "Bob"},
			"+1 5551234567":  {Mid: bobMID, DisplayName: "Bob US"},
		},
	}

	// --userid strips leading @ and lowercases.
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"contacts", "--userid", " @Alice_123 ", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.useridLookups, []string{"alice_123"}) {
		t.Fatalf("userid lookups = %v", f.useridLookups)
	}
	var got contactLookupResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Mid != aliceMID || got.EffectiveName != "Alice" || got.Status != "resolved" {
		t.Fatalf("userid JSON = %s %v", out.String(), err)
	}

	// --userid not found returns clean not_found error.
	out.Reset()
	err := a.Run([]string{"--error-format=json", "contacts", "--userid", "nobody"})
	var machine *MachineError
	if !errors.As(err, &machine) || machine.Detail.Code != "not_found" || machine.Detail.Stage != "lookup" {
		t.Fatalf("userid not-found = %v", err)
	}

	// --phone normalizes compact E.164 and spaced international formats.
	out.Reset()
	if err := a.Run([]string{"contacts", "--phone", "+819012345678"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), bobMID) || !strings.Contains(out.String(), "Bob") {
		t.Fatalf("phone human output = %q", out.String())
	}
	out.Reset()
	if err := a.Run([]string{"contacts", "--phone", "+1 (555) 123-4567", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.phoneLookups, []string{"+81 9012345678", "+1 5551234567"}) {
		t.Fatalf("phone lookups = %v", f.phoneLookups)
	}

	// --phone works in --read-only mode.
	out.Reset()
	if err := a.Run([]string{"--read-only", "contacts", "--phone", "+81 90-1234-5678", "--json"}); err != nil {
		t.Fatalf("read-only phone lookup failed: %v", err)
	}
}

func TestContactDiscoveryAndBlockingValidationBeforeSession(t *testing.T) {
	mid := lookupMID(1)
	cases := [][]string{
		{"--userid", ""}, {"--userid", "@"}, {"--userid", "bad space"}, {"--userid", "bad/slash"},
		{"--phone", ""}, {"--phone", "09012345678"}, {"--phone", "+012345678"}, {"--phone", "+123"},
		{"--phone", "+81 abcdefg"}, {"--userid", "alice", "--phone", "+819012345678"},
		{"--userid", "alice", "--mid", mid}, {"--userid", "alice", "--blocked"},
		{"--phone", "+819012345678", "--search", "Bob"}, {"--blocked", "--limit", "5"},
		{"block"}, {"block", "Alice"}, {"block", "c" + mid[1:]}, {"block", "r" + strings.Repeat("a", 43)},
		{"unblock", "c" + mid[1:]}, {"block", mid, "extra"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, out, _ := testApp(nil)
			a.Lock = func() (func(), error) {
				t.Fatal("opened session before argument validation")
				return nil, nil
			}
			if err := a.Run(append([]string{"contacts"}, args...)); err == nil || out.Len() != 0 {
				t.Fatalf("invalid arguments accepted: %v", err)
			}
		})
	}
}

func TestContactsBlockedListingAndReadOnly(t *testing.T) {
	u1, u2, u3 := lookupMID(31), lookupMID(32), lookupMID(33)
	f := &lookupAPI{
		blockedIDs:    []string{u1, u2},
		blockedRecIDs: []string{u2, u3},
		contacts: map[string]line.Contact{
			u1: {Mid: u1, DisplayName: "Charlie"},
		},
		buddies: map[string]line.BuddyProfile{
			u2: {Mid: u2, DisplayName: "Biz Account"},
		},
	}
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"--read-only", "contacts", "--blocked", "--show-ids"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"BLOCKED CONTACT", "Biz Account", u2, "Charlie", u1, u3, "Showing 3 blocked contacts."} {
		if !strings.Contains(got, want) {
			t.Fatalf("blocked human output missing %q: %q", want, got)
		}
	}

	out.Reset()
	if err := a.Run([]string{"contacts", "--blocked", "--json"}); err != nil {
		t.Fatal(err)
	}
	var rows []line.Contact
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("blocked JSON = %s %v", out.String(), err)
	}
	if rows[0].Mid != u2 || rows[1].Mid != u1 || rows[2].Mid != u3 {
		t.Fatalf("unexpected blocked sort order: %+v", rows)
	}
}

func TestContactsBlockAndUnblockMutations(t *testing.T) {
	target := lookupMID(40)
	f := &lookupAPI{}
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }

	if err := a.Run([]string{"contacts", "block", target, "--json"}); err != nil {
		t.Fatal(err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(f.blockCalls) != 1 || f.blockCalls[0].seq <= 0 || f.blockCalls[0].mid != target || !f.blockCalls[0].blocked {
		t.Fatalf("unexpected block call: %+v", f.blockCalls)
	}
	if receipt["action"] != "block_contact" || receipt["chat_id"] != target || receipt["request_sequence"] != float64(f.blockCalls[0].seq) {
		t.Fatalf("unexpected block receipt: %v", receipt)
	}

	out.Reset()
	if err := a.Run([]string{"contacts", "unblock", target}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Unblocked "+target) || len(f.blockCalls) != 2 || f.blockCalls[1].seq <= f.blockCalls[0].seq || f.blockCalls[1].blocked {
		t.Fatalf("unexpected unblock: out=%q calls=%+v", out.String(), f.blockCalls)
	}

	// Self-block is rejected before reserving a sequence or calling the API.
	selfMID := lookupMID(99)
	a.Manager.Store = &testStore{state: &session.State{AccessToken: "tok", MID: selfMID}}
	err := a.Run([]string{"--error-format=json", "contacts", "block", selfMID})
	var machine *MachineError
	if !errors.As(err, &machine) || machine.Detail.Code != "invalid_arguments" || len(f.blockCalls) != 2 {
		t.Fatalf("self-block not rejected: %v calls=%d", err, len(f.blockCalls))
	}

	// Remote failure reports single-attempt outcome without retrying.
	a.Manager.Store = &testStore{state: &session.State{AccessToken: "tok", MID: selfMID, LastReqSeq: 10}}
	f.blockErr = &line.ResponseError{Status: 403}
	err = a.Run([]string{"--error-format=json", "contacts", "block", target, "--json"})
	if !errors.As(err, &machine) || machine.Detail.Code != "authentication_failed" {
		t.Fatalf("expected auth rejection: %v", err)
	}
	if len(f.blockCalls) != 3 {
		t.Fatalf("mutation retried: %d calls", len(f.blockCalls))
	}
	if op := machine.Detail.Mutation; op == nil || op.Action != "block_contact" || op.ChatID != target || !op.Attempted || op.Outcome != "rejected" || op.RequestSequence == nil || *op.RequestSequence != f.blockCalls[2].seq {
		t.Fatalf("unexpected mutation outcome: %+v", op)
	}

	// --read-only with --error-format=json reports block_contact/unblock_contact not_attempted.
	for _, sub := range []string{"block", "unblock"} {
		roApp, roOut := sealedApp(t, false)
		err := roApp.Run([]string{"--read-only", "--error-format=json", "contacts", sub, target})
		if !errors.As(err, &machine) || machine.Detail.Code != "read_only" || machine.Detail.Stage != "preparation" || roOut.Len() != 0 {
			t.Fatalf("read-only %s = %v", sub, err)
		}
		if op := machine.Detail.Mutation; op == nil || op.Action != sub+"_contact" || op.Attempted || op.Outcome != "not_attempted" || op.RequestSequence != nil {
			t.Fatalf("read-only %s mutation = %+v", sub, op)
		}
	}
}
