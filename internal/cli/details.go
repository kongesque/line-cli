package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// Name/profile transport failures are optional enrichment failures. Local
// storage/session failures and cancellation must retain their exit semantics.
func enrichmentError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var remote *session.RemoteError
	if errors.As(err, &remote) || errors.Is(err, errNoNames) {
		return nil
	}
	return err
}

type namedIdentity struct {
	ID         string  `json:"id"`
	Name       *string `json:"name"`
	NameStatus string  `json:"name_status"`
}

func identity(id, name, status string) namedIdentity {
	result := namedIdentity{ID: id, NameStatus: status}
	if strings.TrimSpace(name) != "" && len(name) <= 64*1024 && utf8.ValidString(name) {
		result.Name = &name
		result.NameStatus = "resolved"
	}
	return result
}

type detailedMessage struct {
	messaging.Message
	ChatID  string            `json:"chat_id"`
	Sender  namedIdentity     `json:"sender"`
	Content messaging.Content `json:"content"`
}
type detailedHistory struct {
	SchemaVersion int               `json:"schema_version"`
	Chat          namedIdentity     `json:"chat"`
	Order         string            `json:"order"`
	Messages      []detailedMessage `json:"messages"`
	NextCursor    string            `json:"next_cursor,omitempty"`
	Exhausted     *bool             `json:"exhausted,omitempty"`
	StopReason    string            `json:"stop_reason,omitempty"`
}

func (a *App) senderNames(messages []messaging.Message) (map[string]string, error) {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.From)
	}
	return a.profileNames(ids)
}

func (a *App) profileNames(mids []string) (map[string]string, error) {
	names := make(map[string]string)
	seen := make(map[string]bool)
	var ids []string
	for _, mid := range mids {
		if mid != "" && !seen[mid] {
			seen[mid] = true
			ids = append(ids, mid)
		}
	}
	for start := 0; start < len(ids); start += 100 {
		var response *line.ContactsResponse
		err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetContactsV2Context(a.loginContext(), ids[start:min(start+100, len(ids))])
			return
		})
		if a.loginContext().Err() != nil {
			return nil, a.loginContext().Err()
		}
		if fatal := enrichmentError(err); fatal != nil {
			return nil, fatal
		}
		if err == nil && response != nil {
			requested := make(map[string]bool)
			for _, id := range ids[start:min(start+100, len(ids))] {
				requested[id] = true
			}
			for id, wrapper := range response.Contacts {
				if !requested[id] || (wrapper.Contact.Mid != "" && wrapper.Contact.Mid != id) {
					continue
				}
				names[id] = wrapper.Contact.EffectiveDisplayName()
			}
		}
	}
	return names, nil
}

func (a *App) detailedMessages(chat string, messages []messaging.Message, page *messaging.HistoryPage, resolveNames bool) (*detailedHistory, error) {
	chats := []Chat{{ID: chat}}
	// Enrichment failures don't erase successfully read messages.
	if err := enrichmentError(a.nameChats(chats)); err != nil {
		return nil, err
	}
	if a.loginContext().Err() != nil {
		return nil, a.loginContext().Err()
	}
	result := &detailedHistory{SchemaVersion: 1, Chat: identity(chat, chats[0].Name, "unavailable"), Order: "server", Messages: make([]detailedMessage, 0, len(messages))}
	names := map[string]string{}
	status := "not_requested"
	if resolveNames {
		var err error
		names, err = a.senderNames(messages)
		if err != nil {
			return nil, err
		}
		status = "unavailable"
	}
	for _, m := range messages {
		// The detail projection uses authoritative decoded fields and explicit status.
		copy := m
		copy.Status = m.Content.Status
		copy.Error = ""
		copy.Text = m.Content.Text
		copy.FileName = ""
		if m.Content.Attachment != nil && m.Content.Attachment.Name != nil {
			copy.FileName = *m.Content.Attachment.Name
		}
		result.Messages = append(result.Messages, detailedMessage{Message: copy, ChatID: m.ChatID, Sender: identity(m.From, names[m.From], status), Content: m.Content})
	}
	if page != nil {
		result.Order = page.Order
		result.NextCursor = page.NextCursor
		result.Exhausted = &page.Exhausted
		result.StopReason = page.StopReason
	}
	return result, nil
}

type detailedChat struct {
	namedIdentity
	Type        int          `json:"type"`
	UnreadCount *json.Number `json:"unread_count"`
	UpdatedAt   *int64       `json:"updated_at"`
}

func (a *App) writeDetailedChats(chats []Chat) error {
	rows := make([]detailedChat, 0, len(chats))
	for _, c := range chats {
		row := detailedChat{namedIdentity: identity(c.ID, c.Name, "unavailable"), Type: c.Type}
		if c.UnreadKnown {
			n := c.UnreadCount
			row.UnreadCount = &n
		}
		if c.UpdatedAt > 0 {
			n := c.UpdatedAt
			row.UpdatedAt = &n
		}
		rows = append(rows, row)
	}
	return a.json(struct {
		SchemaVersion int            `json:"schema_version"`
		Chats         []detailedChat `json:"chats"`
	}{1, rows})
}
