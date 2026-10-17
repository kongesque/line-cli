package line

import (
	"strings"
	"testing"
)

func TestGetContactsV2RequestAndResponse(t *testing.T) {
	var body string
	client := newReactionTestClientWithResponse(t,
		"/api/talk/thrift/Talk/TalkService/getContactsV2",
		`{"code":0,"data":{"contacts":{"u-first":{"contact":{"displayName":"Alice","displayNameOverridden":"Custom"}},"u-blank":{"contact":{"mid":"u-blank"}}}}}`,
		&body,
	)
	response, err := client.GetContactsV2([]string{"u-first", "u-missing", "u-blank"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"targetUserMids":["u-first","u-missing","u-blank"]},2]`
	if body != want {
		t.Fatalf("request = %s, want %s", body, want)
	}
	if len(response.Contacts) != 2 || response.Contacts["u-first"].Contact.EffectiveDisplayName() != "Custom" || response.Contacts["u-blank"].Contact.Mid != "u-blank" {
		t.Fatal("contact details were lost")
	}
	if _, ok := response.Contacts["u-missing"]; ok {
		t.Fatal("omitted contact was fabricated")
	}
}

func TestGetContactsV2DistinguishesEmptyFromMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"empty map", `{"code":0,"data":{"contacts":{}}}`, true},
		{"missing data", `{"code":0}`, false},
		{"missing map", `{"code":0,"data":{}}`, false},
		{"null map", `{"code":0,"data":{"contacts":null}}`, false},
		{"null entry", `{"code":0,"data":{"contacts":{"private-mid":null}}}`, false},
		{"missing contact", `{"code":0,"data":{"contacts":{"private-mid":{}}}}`, true},
		{"null contact", `{"code":0,"data":{"contacts":{"private-mid":{"contact":null}}}}`, true},
		{"wrong contact type", `{"code":0,"data":{"contacts":{"private-mid":{"contact":"private-profile"}}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newReactionTestClientWithResponse(t,
				"/api/talk/thrift/Talk/TalkService/getContactsV2", tc.body, nil)
			response, err := client.GetContactsV2([]string{"private-mid"})
			if tc.ok {
				if err != nil || response == nil || response.Contacts == nil || len(response.Contacts) != 0 {
					t.Fatalf("valid empty response rejected: %v", err)
				}
			} else if err == nil || response != nil || strings.Contains(err.Error(), "private-mid") || strings.Contains(err.Error(), "private-profile") {
				t.Fatalf("malformed response accepted or exposed: %v", err)
			}
		})
	}
}

func TestGetContactsV2UnavailableProfilesDoNotDiscardResolvedContacts(t *testing.T) {
	client := newReactionTestClientWithResponse(t,
		"/api/talk/thrift/Talk/TalkService/getContactsV2",
		`{"code":0,"data":{"contacts":{"u-available":{"contact":{"mid":"u-available","displayName":"Alice"}},"u-null":{"contact":null},"u-missing":{}}}}`, nil)
	response, err := client.GetContactsV2([]string{"u-available", "u-null", "u-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Contacts) != 1 || response.Contacts["u-available"].Contact.EffectiveDisplayName() != "Alice" {
		t.Fatal("an unavailable profile discarded a resolved contact")
	}
	for _, mid := range []string{"u-null", "u-missing"} {
		if _, ok := response.Contacts[mid]; ok {
			t.Fatal("unavailable profile became a resolved contact")
		}
	}
}
