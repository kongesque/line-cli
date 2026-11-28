package line

import (
	"encoding/json"
	"testing"
)

func TestFlexibleMidMapUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []string
	}{
		{
			name: "string valued object",
			data: `{"U-one":"first","U-two":"second"}`,
			want: []string{"U-one", "U-two"},
		},
		{
			name: "boolean valued object",
			data: `{"U-one":true,"U-two":false}`,
			want: []string{"U-one", "U-two"},
		},
		{
			name: "array",
			data: `["U-one","U-two"]`,
			want: []string{"U-one", "U-two"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got FlexibleMidMap
			if err := json.Unmarshal([]byte(test.data), &got); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("decoded MIDs = %v, want %v", got, test.want)
			}
			for _, mid := range test.want {
				if !got[mid] {
					t.Errorf("decoded MIDs = %v, missing %s", got, mid)
				}
			}
		})
	}
}

func TestMessageUnmarshalsRecentMessageReactions(t *testing.T) {
	var msg Message
	err := json.Unmarshal([]byte(`{
		"id":"616934195205767730",
		"reactions":[
			{
				"fromUserMid":"U-predefined",
				"atMillis":"1784930400123",
				"reactionType":{"predefinedReactionType":2}
			},
			{
				"fromUserMid":"U-paid",
				"atMillis":1784930400456,
				"reactionType":{
					"paidReactionType":{
						"productId":"670e0cce840a8236ddd4ee4c",
						"emojiId":"143",
						"resourceType":1,
						"version":"1"
					}
				}
			}
		]
	}`), &msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Reactions) != 2 {
		t.Fatalf("reaction count = %d, want 2", len(msg.Reactions))
	}
	if got := msg.Reactions[0]; got.FromUserMID != "U-predefined" ||
		got.AtMillis.String() != "1784930400123" ||
		got.ReactionType.PredefinedReactionType != 2 {
		t.Fatalf("predefined reaction = %#v", got)
	}
	paid := msg.Reactions[1]
	if paid.FromUserMID != "U-paid" || paid.AtMillis.String() != "1784930400456" ||
		paid.ReactionType.PaidReactionType == nil ||
		paid.ReactionType.PaidReactionType.ProductID != "670e0cce840a8236ddd4ee4c" ||
		paid.ReactionType.PaidReactionType.EmojiID != "143" ||
		paid.ReactionType.PaidReactionType.Version != 1 {
		t.Fatalf("paid reaction = %#v", paid)
	}
}

func TestPaidReactionTypeUnmarshalsNumericVersion(t *testing.T) {
	var reaction PaidReactionType
	if err := json.Unmarshal([]byte(`{"version":1}`), &reaction); err != nil {
		t.Fatal(err)
	}
	if reaction.Version != 1 {
		t.Fatalf("version = %d, want 1", reaction.Version)
	}
}

func TestMessageDistinguishesMissingAndEmptyReactions(t *testing.T) {
	var missing Message
	if err := json.Unmarshal([]byte(`{"id":"missing"}`), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.Reactions != nil {
		t.Fatalf("missing reactions decoded as %#v, want nil", missing.Reactions)
	}

	var empty Message
	if err := json.Unmarshal([]byte(`{"id":"empty","reactions":[]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Reactions == nil || len(empty.Reactions) != 0 {
		t.Fatalf("empty reactions decoded as %#v, want non-nil empty slice", empty.Reactions)
	}
}

func TestMembershipMapPresenceAndMalformedValues(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		present bool
		invalid bool
	}{
		{`{}`, false, false}, {`{"memberMids":null}`, false, false},
		{`{"memberMids":{}}`, true, false}, {`{"memberMids":[]}`, true, false},
		{`{"memberMids":"invalid"}`, false, true}, {`{"memberMids":42}`, false, true},
		{`{"memberMids":[42]}`, false, true},
	} {
		var group GroupExtra
		err := json.Unmarshal([]byte(tc.raw), &group)
		if (err != nil) != tc.invalid || (group.MemberMids != nil) != tc.present {
			t.Fatalf("raw=%s err=%v map=%v", tc.raw, err, group.MemberMids)
		}
	}
}

func TestMessageLocationUnmarshalQuotedAndNumericCoordinates(t *testing.T) {
	valid := []struct {
		name     string
		raw      string
		lat, lng float64
	}{
		{"numeric", `{"title":"Tokyo","address":"JP","latitude":35.6762,"longitude":139.6503}`, 35.6762, 139.6503},
		{"quoted strings", `{"title":"Tokyo","address":"JP","latitude":"35.6762","longitude":"139.6503"}`, 35.6762, 139.6503},
		{"negative quoted", `{"latitude":"-33.8688","longitude":"-151.2093"}`, -33.8688, -151.2093},
		{"zero quoted", `{"latitude":"0","longitude":"0"}`, 0, 0},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			var loc MessageLocation
			if err := json.Unmarshal([]byte(tc.raw), &loc); err != nil {
				t.Fatal(err)
			}
			if loc.Malformed() || loc.Latitude != tc.lat || loc.Longitude != tc.lng {
				t.Fatalf("got %+v (malformed=%v), want (%v,%v)", loc, loc.Malformed(), tc.lat, tc.lng)
			}
		})
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{"missing", `{}`},
		{"null", `{"latitude":null,"longitude":1}`},
		{"non-numeric string", `{"latitude":"abc","longitude":"1"}`},
		{"NaN string", `{"latitude":"NaN","longitude":"1"}`},
		{"Inf string", `{"latitude":"Inf","longitude":"1"}`},
		{"overflow", `{"latitude":"1e309","longitude":"1"}`},
		{"lat out of range", `{"latitude":"90.1","longitude":"0"}`},
		{"lng out of range", `{"latitude":"0","longitude":"-180.1"}`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			var loc MessageLocation
			if err := json.Unmarshal([]byte(tc.raw), &loc); err != nil {
				t.Fatal(err)
			}
			if !loc.Malformed() {
				t.Fatalf("accepted invalid location %s: %+v", tc.raw, loc)
			}
		})
	}
}
