package messaging

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var mentionMID = regexp.MustCompile(`^(?:[uU][A-Za-z0-9_-]{43}|u[0-9a-f]{32})$`)

// ParseMentions accepts the same safe field names as detailed incoming content.
// Require explicit bounds and reject unknown fields so typos cannot change who
// is notified. Errors deliberately omit the supplied text, IDs, and JSON.
func ParseMentions(raw string) ([]Mention, error) {
	invalid := errors.New("--mentions requires a JSON array of up to 100 objects with start, end, and either mid or all:true")
	if len(raw) > maxContentBytes {
		return nil, invalid
	}
	var rows []struct {
		Start *int   `json:"start"`
		End   *int   `json:"end"`
		MID   string `json:"mid"`
		All   bool   `json:"all"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&rows) != nil || rows == nil || len(rows) > 100 {
		return nil, invalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, invalid
	}
	result := make([]Mention, 0, len(rows))
	for _, row := range rows {
		if row.Start == nil || row.End == nil {
			return nil, invalid
		}
		result = append(result, Mention{Start: *row.Start, End: *row.End, MID: row.MID, All: row.All})
	}
	return result, nil
}

// EncodeMentions validates UTF-16 boundaries against the final unmodified text.
// Chrome keeps this metadata outside the Letter Sealing encrypted body.
func EncodeMentions(text string, mentions []Mention) (string, error) {
	if len(mentions) == 0 {
		return "", nil
	}
	invalid := errors.New("invalid mentions: use full user MIDs or all:true, and nonoverlapping UTF-16 spans within the text")
	if len(mentions) > 100 {
		return "", invalid
	}
	type mention struct {
		Start string `json:"S"`
		End   string `json:"E"`
		MID   string `json:"M,omitempty"`
		All   string `json:"A,omitempty"`
	}
	rows := make([]mention, 0, len(mentions))
	for _, m := range mentions {
		if (!m.All && !mentionMID.MatchString(m.MID)) || (m.All && m.MID != "") {
			return "", invalid
		}
		row := mention{Start: strconv.Itoa(m.Start), End: strconv.Itoa(m.End), MID: m.MID}
		if m.All {
			row.All = "1"
		}
		rows = append(rows, row)
	}
	data, err := json.Marshal(struct {
		Mentions []mention `json:"MENTIONEES"`
	}{rows})
	if err != nil {
		return "", invalid
	}
	if _, ok := decodeMentions(string(data), text); !ok {
		return "", invalid
	}
	return string(data), nil
}
