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
	contacts  map[string]line.Contact
	batches   [][]string
	listCalls int
	response  func([]string) (*line.ContactsResponse, error)
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
	if out.Len() != 0 || !strings.Contains(diagnostics.String(), "--mid MID") || !strings.Contains(diagnostics.String(), "non-friends") {
		t.Fatal("lookup help missing")
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
