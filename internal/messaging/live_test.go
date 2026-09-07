package messaging

import (
	"os"
	"sort"
	"testing"

	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

// Explicitly opt in after signing in. Reports aggregate statuses only. Does not
// send messages, register group keys, mark read, or write message data to disk.
func TestLiveHistory(t *testing.T) {
	if os.Getenv("LINE_CLI_LIVE_READ") != "1" {
		t.Skip("set LINE_CLI_LIVE_READ=1 to validate saved-session read access")
	}
	unlock, err := session.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	manager := session.NewManager(session.KeychainStore{})
	client, err := New(manager)
	if err != nil {
		t.Fatal(err)
	}
	options := line.MessageBoxesOptions{ActiveOnly: true, MessageBoxCountLimit: 100, LastMessagesPerMessageBoxCount: 1}
	var boxes []line.MessageBox
	cursors := map[string]bool{}
	for page := 0; page < 5; page++ {
		var response *line.MessageBoxesResponse
		if err := manager.Do(func(api session.API) (err error) { response, err = api.GetMessageBoxes(options); return }); err != nil {
			t.Fatal(err)
		}
		if response == nil {
			t.Fatal("empty active chat response")
		}
		boxes = append(boxes, response.MessageBoxes...)
		if !response.HasNext || len(response.MessageBoxes) == 0 {
			break
		}
		cursor := response.MessageBoxes[len(response.MessageBoxes)-1].ID
		if cursors[cursor] {
			break
		}
		cursors[cursor], options.MinChatID = true, cursor
	}
	stamp := func(b line.MessageBox) int64 {
		if len(b.LastMessages) == 0 {
			return 0
		}
		n, _ := b.LastMessages[0].CreatedTime.Int64()
		return n
	}
	encryptedPreview := func(b line.MessageBox) bool {
		return len(b.LastMessages) > 0 && (len(b.LastMessages[0].Chunks) > 0 || b.LastMessages[0].ContentMetadata["e2eeVersion"] != "")
	}
	sort.SliceStable(boxes, func(i, j int) bool {
		if encryptedPreview(boxes[i]) != encryptedPreview(boxes[j]) {
			return encryptedPreview(boxes[i])
		}
		return stamp(boxes[i]) > stamp(boxes[j])
	})
	withPreview := 0
	for _, box := range boxes {
		if len(box.LastMessages) > 0 {
			withPreview++
		}
	}
	t.Logf("active chats: %d; chats with previews: %d; own keys restored: %v", len(boxes), withPreview, client.crypto != nil)
	verified := map[int]bool{}
	tried := map[int]int{}
	for _, box := range boxes {
		if ValidateChatID(box.ID) != nil {
			continue
		}
		kind := toType(box.ID)
		if kind == 1 {
			kind = 2
		}
		if verified[kind] || tried[kind] >= 8 {
			continue
		}
		tried[kind]++
		messages, err := client.History(box.ID, 10)
		if err != nil {
			t.Logf("type %d sample %d: %v", kind, tried[kind], err)
			continue
		}
		counts := map[string]int{}
		for _, m := range messages {
			counts[m.Status]++
		}
		t.Logf("type %d sample %d: %d messages; statuses: %v", kind, tried[kind], len(messages), counts)
		if counts["decrypted"] > 0 {
			verified[kind] = true
		}
		if verified[0] && verified[2] {
			return
		}
	}
	if !verified[0] || !verified[2] {
		t.Fatalf("live encrypted history not verified for both direct and group chats (direct=%v group=%v)", verified[0], verified[2])
	}
}
