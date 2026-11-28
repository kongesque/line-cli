package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func messagePreview(m messaging.Message) string {
	return strings.Join(messageLines(m), "\n")
}

func messageLines(m messaging.Message) []string {
	raw := messageRawPreview(m)
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = terminalText(line)
	}
	return lines
}

func messageRawPreview(m messaging.Message) string {
	if m.Content.Status == "decryption_failed" || m.Error != "" {
		return "[Unable to decrypt this message]"
	}
	if m.Content.Post != nil {
		return formatPostNotification(m.Content.Post)
	}
	if m.Content.Status == "malformed_metadata" {
		return "[Malformed message content]"
	}
	if m.Content.Location != nil {
		l := m.Content.Location
		return fmt.Sprintf("[Location: %s · %s · %.6f, %.6f]", terminalText(l.Title), terminalText(l.Address), l.Latitude, l.Longitude)
	}
	if m.Content.Contact != nil {
		return "[Contact: " + terminalText(m.Content.Contact.DisplayName) + " · " + terminalText(m.Content.Contact.MID) + "]"
	}
	if m.Content.Rich != nil && (m.Content.Rich.AltText != "" || len(m.Content.Rich.Text) > 0 || len(m.Content.Rich.Links) > 0) {
		var parts []string
		if m.Content.Rich.AltText != "" {
			parts = append(parts, m.Content.Rich.AltText)
		}
		parts = append(parts, m.Content.Rich.Text...)
		parts = append(parts, m.Content.Rich.Links...)
		return strings.Join(parts, "\n")
	}
	if m.Content.Sticker != nil {
		return "[Sticker: " + terminalText(m.Content.Sticker.PackageID) + "/" + terminalText(m.Content.Sticker.ID) + "]"
	}
	if m.Content.Attachment != nil && m.Content.Attachment.Name != nil {
		kind := map[string]string{"file": "File", "image": "Image", "video": "Video", "audio": "Audio"}[m.Content.Attachment.Type]
		return "[" + kind + ": " + terminalText(*m.Content.Attachment.Name) + "]"
	}
	if m.ContentType == 14 && m.Content.Attachment != nil {
		return "[File: (name unavailable)]"
	}
	if m.ContentType == 14 {
		return "[File: " + terminalText(m.FileName) + "]"
	}
	if m.ContentType != 0 {
		kind := map[int]string{1: "Image", 2: "Video", 3: "Audio", 7: "Sticker", 13: "Contact", 16: "Post notification", 22: "Rich message"}[m.ContentType]
		if kind == "" {
			kind = fmt.Sprintf("Content type %d", m.ContentType)
		}
		return "[" + kind + "]"
	}
	if m.Content.Kind == "" || m.Content.Kind == "text" {
		return m.Text
	}
	return ""
}

func formatPostNotification(p *messaging.PostNotification) string {
	var lines []string
	switch p.Type {
	case "note":
		if p.Text != "" {
			textLines := strings.Split(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n")
			if strings.TrimSpace(textLines[0]) != "" {
				lines = append(lines, "[Note: "+textLines[0]+"]")
			} else {
				lines = append(lines, "[Note]")
			}
			lines = append(lines, textLines[1:]...)
		} else {
			lines = append(lines, "[Note]")
		}
	case "album":
		if p.Title != "" {
			lines = append(lines, "[Album: "+p.Title+"]")
		} else {
			lines = append(lines, "[Album]")
		}
		if p.Text != "" {
			lines = append(lines, strings.Split(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n")...)
		}
	default:
		if p.Title != "" {
			lines = append(lines, "[Post notification: "+p.Title+"]")
			if p.Text != "" {
				lines = append(lines, strings.Split(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n")...)
			}
		} else if p.Text != "" {
			textLines := strings.Split(strings.ReplaceAll(p.Text, "\r\n", "\n"), "\n")
			if strings.TrimSpace(textLines[0]) != "" {
				lines = append(lines, "[Post notification: "+textLines[0]+"]")
			} else {
				lines = append(lines, "[Post notification]")
			}
			lines = append(lines, textLines[1:]...)
		} else {
			lines = append(lines, "[Post notification]")
		}
	}
	if p.URL != "" {
		lines = append(lines, p.URL)
	}
	return strings.Join(lines, "\n")
}

func (a *App) renderMessages(chat string, messages []messaging.Message, ids bool) error {
	if len(messages) == 0 {
		_, err := fmt.Fprintln(a.Out, "No recent messages in "+terminalText(chat)+".")
		return err
	}
	names := make(map[string]string)
	if s, err := a.Manager.Store.Load(); err == nil && s != nil {
		names[s.MID] = "You"
	}
	var senders []string
	seen := make(map[string]bool)
	for _, m := range messages {
		if m.From != "" && names[m.From] == "" && !seen[m.From] {
			senders = append(senders, m.From)
			seen[m.From] = true
		}
	}
	if len(senders) > 0 {
		var response *line.ContactsResponse
		err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetContactsV2Context(a.loginContext(), senders)
			return
		})
		if a.loginContext().Err() != nil {
			return a.loginContext().Err()
		}
		if err == nil && response != nil {
			for id, c := range response.Contacts {
				names[id] = c.Contact.EffectiveDisplayName()
			}
			var missing []string
			for _, id := range senders {
				if strings.TrimSpace(names[id]) == "" {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				if buddies, buddyErr := a.fetchBuddyProfiles(a.loginContext(), missing); buddyErr == nil {
					for id, c := range buddies {
						names[id] = c.EffectiveDisplayName()
					}
				} else if a.loginContext().Err() != nil {
					return a.loginContext().Err()
				}
			}
		} else {
			fmt.Fprintln(a.Err, "Some sender names are unavailable; showing IDs instead.")
		}
	}
	ordered := append([]messaging.Message(nil), messages...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, _ := ordered[i].CreatedTime.Int64()
		b, _ := ordered[j].CreatedTime.Int64()
		return a < b
	})
	if _, err := fmt.Fprintf(a.Out, "%s · %d recent messages\n", terminalText(chat), len(ordered)); err != nil {
		return err
	}
	date := ""
	for _, m := range ordered {
		stamp, _ := m.CreatedTime.Int64()
		tm := time.UnixMilli(stamp).Local()
		day, clock := tm.Format("Mon, 2 Jan 2006"), tm.Format("15:04")
		if stamp <= 0 {
			day, clock = "Date unavailable", "—"
		}
		if day != date {
			if _, err := fmt.Fprintln(a.Out, "\n"+day); err != nil {
				return err
			}
			date = day
		}
		name := names[m.From]
		if name == "" {
			name = m.From
		}
		if name == "" {
			name = "Unknown sender"
		}
		if _, err := fmt.Fprintf(a.Out, "%s  %s\n", clock, terminalText(name)); err != nil {
			return err
		}
		if ids {
			if _, err := fmt.Fprintln(a.Out, "       ID: "+terminalText(m.ID)); err != nil {
				return err
			}
		}
		if m.ReplyTo != "" {
			note := "       ↳ Reply"
			if ids {
				note += " to " + terminalText(m.ReplyTo)
			}
			if _, err := fmt.Fprintln(a.Out, note); err != nil {
				return err
			}
		}
		// Preserve line breaks; terminals wrap Unicode using their own cell widths.
		for _, line := range messageLines(m) {
			if _, err := fmt.Fprintln(a.Out, "       "+line); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(a.Out); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(a.Out, "Write: line send %q\n", terminalText(chat))
	return err
}
