package line

import (
	"context"
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

func TestGetBuddyProfileRoomsV2AndLogoutContracts(t *testing.T) {
	t.Run("GetBuddyProfileContext", func(t *testing.T) {
		var body string
		client := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/BuddyService/getBuddyProfile",
			`{"code":0,"data":{"buddyMid":"u-official","displayName":"LINE Official","picturePath":"/pic"}}`,
			&body,
		)
		profile, err := client.GetBuddyProfileContext(context.Background(), "u-official")
		if err != nil {
			t.Fatal(err)
		}
		if body != `["u-official"]` || profile.Mid != "u-official" || profile.DisplayName != "LINE Official" {
			t.Fatalf("unexpected buddy profile: body=%s profile=%+v", body, profile)
		}

		notFound := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/BuddyService/getBuddyProfile",
			`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":5,"reason":"not found","secret":"hide"}}`,
			nil,
		)
		_, err = notFound.GetBuddyProfileContext(context.Background(), "u-normal")
		if !IsBuddyNotFound(err) || strings.Contains(err.Error(), "hide") {
			t.Fatalf("buddy not-found misclassified or leaked: %v", err)
		}
	})

	t.Run("GetRoomsV2Context", func(t *testing.T) {
		var body string
		client := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/getRoomsV2",
			`{"code":0,"data":[{"mid":"r-room1","memberMids":{"u-a":true,"u-b":true}}]}`,
			&body,
		)
		rooms, err := client.GetRoomsV2Context(context.Background(), []string{"r-room1"})
		if err != nil {
			t.Fatal(err)
		}
		if body != `[["r-room1"]]` || len(rooms) != 1 || rooms[0].Mid != "r-room1" || !rooms[0].MemberMids["u-a"] {
			t.Fatalf("unexpected rooms: body=%s rooms=%+v", body, rooms)
		}
	})

	t.Run("LogoutContext", func(t *testing.T) {
		var body string
		client := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/AuthService/logoutV2",
			`{"code":0,"data":null}`,
			&body,
		)
		if err := client.LogoutContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		if body != `[]` {
			t.Fatalf("logoutV2 body = %s, want []", body)
		}

		failClient := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/AuthService/logoutV2",
			`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":8,"reason":"V3_TOKEN_CLIENT_LOGGED_OUT","secret":"hide"}}`,
			nil,
		)
		err := failClient.LogoutContext(context.Background())
		if !IsLoggedOut(err) || strings.Contains(err.Error(), "hide") {
			t.Fatalf("logoutV2 error misclassified or leaked: %v", err)
		}
	})
}

func TestContactDiscoveryAndBlockingWireContracts(t *testing.T) {
	t.Run("FindContactByUseridContext", func(t *testing.T) {
		var body string
		client := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/findContactByUserid",
			`{"code":0,"data":{"mid":"u-found","displayName":"Alice"}}`,
			&body,
		)
		contact, err := client.FindContactByUseridContext(context.Background(), "alice")
		if err != nil {
			t.Fatal(err)
		}
		if body != `["alice"]` || contact.Mid != "u-found" || contact.DisplayName != "Alice" {
			t.Fatalf("unexpected userid lookup: body=%s contact=%+v", body, contact)
		}

		notFound := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/findContactByUserid",
			`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":5,"reason":"not found","secret":"hide"}}`,
			nil,
		)
		_, err = notFound.FindContactByUseridContext(context.Background(), "nobody")
		if !IsContactNotFound(err) || strings.Contains(err.Error(), "hide") {
			t.Fatalf("userid not-found misclassified or leaked: %v", err)
		}
	})

	t.Run("FindContactByPhoneContext", func(t *testing.T) {
		var body string
		client := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/findContactsByPhone",
			`{"code":0,"data":{"+81 9012345678":{"mid":"u-phone","displayName":"Bob"}}}`,
			&body,
		)
		contact, err := client.FindContactByPhoneContext(context.Background(), "+81 9012345678")
		if err != nil {
			t.Fatal(err)
		}
		if body != `[["+81 9012345678"]]` || contact.Mid != "u-phone" || contact.DisplayName != "Bob" {
			t.Fatalf("unexpected phone lookup: body=%s contact=%+v", body, contact)
		}

		empty := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/findContactsByPhone",
			`{"code":0,"data":{}}`,
			nil,
		)
		_, err = empty.FindContactByPhoneContext(context.Background(), "+81 9000000000")
		if !IsContactNotFound(err) {
			t.Fatalf("empty phone map should be not-found: %v", err)
		}
	})

	t.Run("GetBlockedRecommendationIdsContext", func(t *testing.T) {
		var body string
		client := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/getBlockedRecommendationIds",
			`{"code":0,"data":["u-rec1","u-rec2"]}`,
			&body,
		)
		ids, err := client.GetBlockedRecommendationIdsContext(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if body != `[]` || len(ids) != 2 || ids[0] != "u-rec1" || ids[1] != "u-rec2" {
			t.Fatalf("unexpected blocked recommendation ids: body=%s ids=%v", body, ids)
		}
	})

	t.Run("SetContactBlockedContext", func(t *testing.T) {
		var blockBody string
		blockClient := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/blockContact",
			`{"code":0,"data":null}`,
			&blockBody,
		)
		if err := blockClient.SetContactBlockedContext(context.Background(), 42, "u-target", true); err != nil {
			t.Fatal(err)
		}
		if blockBody != `[42,"u-target"]` {
			t.Fatalf("blockContact body = %s, want [42,\"u-target\"]", blockBody)
		}

		var unblockBody string
		unblockClient := newReactionTestClientWithResponse(t,
			"/api/talk/thrift/Talk/TalkService/unblockContact",
			`{"code":0,"data":null}`,
			&unblockBody,
		)
		if err := unblockClient.SetContactBlockedContext(context.Background(), 43, "u-target", false); err != nil {
			t.Fatal(err)
		}
		if unblockBody != `[43,"u-target",""]` {
			t.Fatalf("unblockContact body = %s, want [43,\"u-target\",\"\"]", unblockBody)
		}
	})
}
