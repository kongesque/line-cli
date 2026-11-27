package messaging

import (
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/kongesque/line-cli/pkg/line"
)

const (
	maxContentBytes        = 64 * 1024
	maxPostPreviewMedias   = 64
	maxPostIdentifierBytes = 256
)

// Content is an allowlisted view, never a copy of protocol metadata. Status is
// independent of the legacy Message status so existing JSON remains compatible.
type Content struct {
	Kind       string                `json:"kind"`
	Status     string                `json:"status"`
	Text       string                `json:"text,omitempty"`
	Mentions   []Mention             `json:"mentions,omitempty"`
	Location   *line.MessageLocation `json:"location,omitempty"`
	Attachment *AttachmentInfo       `json:"attachment,omitempty"`
	Contact    *SharedContact        `json:"contact,omitempty"`
	Sticker    *Sticker              `json:"sticker,omitempty"`
	Rich       *RichContent          `json:"rich,omitempty"`
	Post       *PostNotification     `json:"post,omitempty"`
}
type PostNotification struct {
	Type         string `json:"type"`
	Title        string `json:"title,omitempty"`
	Text         string `json:"text,omitempty"`
	URL          string `json:"url,omitempty"`
	AlbumID      string `json:"album_id,omitempty"`
	PreviewCount *int   `json:"preview_count,omitempty"`
}
type Mention struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	MID   string `json:"mid,omitempty"`
	All   bool   `json:"all,omitempty"`
}
type AttachmentInfo struct {
	Type       string  `json:"type"`
	Name       *string `json:"name"`
	SizeBytes  *int64  `json:"size_bytes"`
	DurationMS *int64  `json:"duration_ms"`
}
type SharedContact struct {
	MID         string `json:"mid"`
	DisplayName string `json:"display_name"`
}
type Sticker struct {
	PackageID string `json:"package_id"`
	ID        string `json:"id"`
	Text      string `json:"text,omitempty"`
}
type RichContent struct {
	AltText   string   `json:"alt_text,omitempty"`
	Text      []string `json:"text"`
	Links     []string `json:"links"`
	Truncated bool     `json:"truncated"`
}

func contentKind(t int) string {
	if name, ok := map[int]string{0: "text", 1: "image", 2: "video", 3: "audio", 7: "sticker", 13: "contact", 14: "file", 15: "location", 16: "post_notification", 17: "rich", 22: "flex"}[t]; ok {
		return name
	}
	return "unsupported"
}

func decodeContent(chat string, msg *line.Message, encrypted bool, payload string) Content {
	c := Content{Kind: contentKind(msg.ContentType), Status: "ok"}
	if c.Kind == "unsupported" {
		c.Status = "unsupported"
		return c
	}
	if msg.ContentType == 16 {
		if encrypted || hasPostEncryptionMarkers(msg) {
			c.Status = "unsupported"
			return c
		}
		parsed := parsePostNotification(chat, msg)
		c.Status = parsed.status
		c.Post = parsed.post
		return c
	}
	if !encrypted && isPlaintextPostWrapper(msg) {
		parsed := parsePostNotification(chat, msg)
		return Content{Kind: "post_notification", Status: parsed.status, Post: parsed.post}
	}
	text, location, name := msg.Text, msg.Location, msg.ContentMetadata["FILE_NAME"]
	if encrypted {
		// The authenticated body owns these fields. Never fall back to outer values.
		text, location, name = "", nil, ""
		var body struct {
			Text     *string               `json:"text"`
			Location *line.MessageLocation `json:"location"`
			FileName *string               `json:"fileName"`
		}
		if len(payload) > maxContentBytes || !strings.HasPrefix(strings.TrimSpace(payload), "{") || json.Unmarshal([]byte(payload), &body) != nil {
			c.Status = "malformed_metadata"
			return c
		}
		if body.Text != nil {
			text = *body.Text
		}
		location = body.Location
		if body.FileName != nil {
			name = *body.FileName
		}
		if msg.ContentType == 0 && body.Text == nil && location == nil {
			c.Status = "malformed_metadata"
			return c
		}
	}
	bounded := func(s string) string {
		if len(s) > maxContentBytes || !utf8.ValidString(s) {
			c.Status = "malformed_metadata"
			return ""
		}
		return s
	}
	if location != nil && (msg.ContentType == 0 || msg.ContentType == 15) {
		c.Kind = "location"
		if location.Malformed() || math.IsNaN(location.Latitude) || math.IsInf(location.Latitude, 0) || math.IsNaN(location.Longitude) || math.IsInf(location.Longitude, 0) || math.Abs(location.Latitude) > 90 || math.Abs(location.Longitude) > 180 {
			c.Status = "malformed_metadata"
		} else {
			copy := *location
			copy.Title = bounded(copy.Title)
			copy.Address = bounded(copy.Address)
			c.Location = &copy
		}
	} else if msg.ContentType == 15 {
		c.Status = "malformed_metadata"
	}
	switch msg.ContentType {
	case 0:
		c.Text = bounded(text)
		mentions, ok := decodeMentions(msg.ContentMetadata["MENTION"], text)
		c.Mentions = mentions
		if !ok {
			c.Status = "malformed_metadata"
		}
	case 1, 2, 3, 14:
		c.Attachment = &AttachmentInfo{Type: c.Kind}
		if name != "" {
			n := bounded(name)
			if n != "" {
				c.Attachment.Name = &n
			}
		}
		number := func(key string) *int64 {
			s, exists := msg.ContentMetadata[key]
			if !exists {
				return nil
			}
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || n < 0 {
				c.Status = "malformed_metadata"
				return nil
			}
			return &n
		}
		c.Attachment.SizeBytes = number("FILE_SIZE")
		if msg.ContentType == 2 || msg.ContentType == 3 {
			c.Attachment.DurationMS = number("DURATION")
		}
	case 7:
		c.Sticker = &Sticker{PackageID: bounded(msg.ContentMetadata["STKPKGID"]), ID: bounded(msg.ContentMetadata["STKID"]), Text: bounded(msg.ContentMetadata["STKTXT"])}
		if c.Sticker.ID == "" || c.Sticker.PackageID == "" {
			c.Status = "malformed_metadata"
		}
	case 13:
		c.Contact = &SharedContact{MID: bounded(msg.ContentMetadata["mid"]), DisplayName: bounded(msg.ContentMetadata["displayName"])}
		if ValidateChatID(c.Contact.MID) != nil || !strings.HasPrefix(strings.ToLower(c.Contact.MID), "u") {
			c.Status = "malformed_metadata"
			c.Contact = nil
		}
	case 17, 22:
		var ok bool
		c.Rich, ok = decodeRich(msg.ContentMetadata["ALT_TEXT"], msg.ContentMetadata["FLEX_JSON"])
		if !ok {
			c.Status = "malformed_metadata"
		}
		if msg.ContentType == 17 && msg.ContentMetadata["ALT_TEXT"] == "" && msg.ContentMetadata["FLEX_JSON"] == "" {
			c.Status = "unsupported"
		}
	}
	return c
}

func decodeMentions(raw, text string) ([]Mention, bool) {
	if raw == "" {
		return nil, true
	}
	if len(raw) > maxContentBytes || len(text) > maxContentBytes || !utf8.ValidString(text) {
		return nil, false
	}
	var body struct {
		Mentions []struct {
			Start string `json:"S"`
			End   string `json:"E"`
			MID   string `json:"M"`
			All   string `json:"A"`
		} `json:"MENTIONEES"`
	}
	if json.Unmarshal([]byte(raw), &body) != nil || body.Mentions == nil || len(body.Mentions) > 100 {
		return nil, false
	}
	boundaries := map[int]bool{0: true}
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
		boundaries[n] = true
	}
	result := make([]Mention, 0, len(body.Mentions))
	for _, m := range body.Mentions {
		s, e1 := strconv.Atoi(m.Start)
		e, e2 := strconv.Atoi(m.End)
		if (m.All != "" && m.All != "1") || (m.MID != "" && !strings.HasPrefix(strings.ToLower(m.MID), "u")) || e1 != nil || e2 != nil || s >= e || !boundaries[s] || !boundaries[e] || (m.All != "1" && ValidateChatID(m.MID) != nil) || (m.All == "1" && m.MID != "") {
			return nil, false
		}
		for _, p := range result {
			if s < p.End && e > p.Start {
				return nil, false
			}
		}
		result = append(result, Mention{Start: s, End: e, MID: m.MID, All: m.All == "1"})
	}
	return result, true
}

// Walk only known layout children and visible text/action fields. Never scrape
// arbitrary strings, URLs, image resources, postback data, or unknown objects.
func decodeRich(alt, raw string) (*RichContent, bool) {
	r := &RichContent{Text: []string{}, Links: []string{}}
	if len(alt) > maxContentBytes || !utf8.ValidString(alt) || len(raw) > maxContentBytes || !utf8.ValidString(raw) {
		return r, false
	}
	r.AltText = alt
	if raw == "" {
		return r, true
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &root) != nil || root == nil {
		return r, false
	}
	nodes, total := 0, 0
	var walk func(map[string]json.RawMessage, int) bool
	walk = func(node map[string]json.RawMessage, depth int) bool {
		if node == nil {
			return false
		}
		nodes++
		if depth > 16 || nodes > 256 {
			r.Truncated = true
			return true
		}
		str := func(key string) string { var s string; _ = json.Unmarshal(node[key], &s); return s }
		add := func(dst *[]string, s string) {
			if s == "" {
				return
			}
			if total+len(s) > 8192 || len(*dst) >= 100 {
				r.Truncated = true
				return
			}
			total += len(s)
			*dst = append(*dst, s)
		}
		switch str("type") {
		case "text", "span":
			add(&r.Text, str("text"))
		case "uri":
			u, err := url.Parse(str("uri"))
			if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
				add(&r.Links, u.String())
			}
			add(&r.Text, str("label"))
		case "button": // The action label is handled by the action child.
		}
		for _, key := range []string{"header", "hero", "body", "footer", "action", "contents"} {
			value, ok := node[key]
			if !ok {
				continue
			}
			if len(value) > 0 && value[0] == '[' {
				var children []map[string]json.RawMessage
				if json.Unmarshal(value, &children) != nil {
					return false
				}
				for _, child := range children {
					if nodes >= 256 {
						r.Truncated = true
						break
					}
					if !walk(child, depth+1) {
						return false
					}
				}
			} else {
				var child map[string]json.RawMessage
				if json.Unmarshal(value, &child) != nil || child == nil {
					return false
				}
				if !walk(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	return r, walk(root, 0)
}

type parsedPost struct {
	post    *PostNotification
	chatID  string
	albumID string
	oids    []string
	status  string
}

type postURLRoute int

const (
	postRouteNone postURLRoute = iota
	postRoutePost
	postRouteAlbum
)

var allowedPostQueryParams = map[string]bool{
	"postId":    true,
	"homeId":    true,
	"groupId":   true,
	"albumId":   true,
	"albumIdV2": true,
	"source":    true,
	"type":      true,
}

func hasPostEncryptionMarkers(msg *line.Message) bool {
	return msg != nil && (len(msg.Chunks) > 0 || msg.ContentMetadata["e2eeVersion"] != "")
}

func isPlaintextPostWrapper(msg *line.Message) bool {
	return msg != nil &&
		msg.ContentType == 0 &&
		!hasPostEncryptionMarkers(msg) &&
		msg.Location == nil &&
		msg.ContentMetadata["ORGCONTP"] == "POSTNOTIFICATION"
}

func isPostNotificationMessage(msg *line.Message) bool {
	return msg != nil && (msg.ContentType == 16 || isPlaintextPostWrapper(msg))
}

func parsePostNotification(chat string, msg *line.Message) parsedPost {
	if !isPostNotificationMessage(msg) || hasPostEncryptionMarkers(msg) {
		return parsedPost{status: "unsupported"}
	}
	meta := msg.ContentMetadata
	status := "ok"
	markMalformed := func() { status = "malformed_metadata" }

	serviceType := ""
	if raw := meta["serviceType"]; raw != "" {
		if len(raw) > maxContentBytes || !utf8.ValidString(raw) {
			markMalformed()
		} else {
			serviceType = strings.ToUpper(strings.TrimSpace(raw))
		}
	}
	postType := "unknown"
	switch serviceType {
	case "GB":
		postType = "note"
	case "AB":
		postType = "album"
	}

	post := &PostNotification{Type: postType}

	if raw := meta["text"]; raw != "" {
		if len(raw) > maxContentBytes || !utf8.ValidString(raw) {
			markMalformed()
		} else {
			post.Text = raw
		}
	}

	if postType != "note" {
		if raw := meta["albumName"]; raw != "" {
			if len(raw) > maxContentBytes || !utf8.ValidString(raw) {
				markMalformed()
			} else {
				post.Title = strings.TrimSpace(raw)
			}
		}
	}

	albumID := ""
	if raw := meta["postEndUrl"]; raw != "" {
		urlRes := parsePostEndURL(raw, postType == "album")
		if urlRes.malformed {
			markMalformed()
		}
		post.URL = urlRes.url
		post.AlbumID = urlRes.albumID
		albumID = urlRes.albumID
	}

	var chatID string
	var oids []string
	if postType == "album" {
		count := 0
		post.PreviewCount = &count
		if raw := meta["chatId"]; raw != "" {
			if len(raw) > maxContentBytes || !utf8.ValidString(raw) || ValidateChatID(raw) != nil {
				markMalformed()
			} else {
				chatID = raw
			}
		}
		candidates, ok := parsePostPreviewMedias(meta["previewMedias"])
		if !ok {
			markMalformed()
		}
		if chatID != "" && chat != "" && chatID == chat {
			oids = candidates
			n := len(oids)
			post.PreviewCount = &n
		}
	}

	return parsedPost{
		post:    post,
		chatID:  chatID,
		albumID: albumID,
		oids:    oids,
		status:  status,
	}
}

type postURLResult struct {
	url       string
	albumID   string
	malformed bool
}

func parsePostEndURL(raw string, extractAlbumID bool) postURLResult {
	if len(raw) > maxContentBytes || !utf8.ValidString(raw) {
		return postURLResult{malformed: true}
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return postURLResult{malformed: true}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Opaque != "" || u.User != nil || strings.Contains(u.Host, ":") {
		return postURLResult{malformed: true}
	}
	switch u.Scheme {
	case "https":
		if u.Host != "line.me" {
			return postURLResult{malformed: true}
		}
	case "line":
	default:
		return postURLResult{malformed: true}
	}
	if hasUnsafeURLPath(u.Path) || hasUnsafeURLPath(u.RawPath) {
		return postURLResult{malformed: true}
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return postURLResult{malformed: true}
	}
	hasUnknownParam := u.Fragment != ""
	for k, vals := range values {
		if !utf8.ValidString(k) || hasUnsafeURLParamText(k) {
			return postURLResult{malformed: true}
		}
		if !allowedPostQueryParams[k] {
			hasUnknownParam = true
		}
		for _, v := range vals {
			if !utf8.ValidString(v) || hasUnsafeURLParamText(v) {
				return postURLResult{malformed: true}
			}
		}
	}

	route := classifyPostRoute(u)
	var res postURLResult
	if route != postRouteNone && !hasUnknownParam {
		res.url = raw
	}
	if extractAlbumID && route == postRouteAlbum {
		v2Vals := values["albumIdV2"]
		v1Vals := values["albumId"]
		if len(v2Vals) > 1 || len(v1Vals) > 1 {
			res.malformed = true
			return res
		}
		if len(v2Vals) == 1 && v2Vals[0] != "" && !isValidPostAlbumID(v2Vals[0]) {
			res.malformed = true
			return res
		}
		if len(v1Vals) == 1 && v1Vals[0] != "" && !isValidPostAlbumID(v1Vals[0]) {
			res.malformed = true
			return res
		}
		if len(v2Vals) == 1 && v2Vals[0] != "" {
			res.albumID = v2Vals[0]
		} else if len(v1Vals) == 1 && v1Vals[0] != "" {
			res.albumID = v1Vals[0]
		}
	}
	return res
}

func hasUnsafeURLPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.Contains(p, "\\") || strings.Contains(p, "//") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

func hasUnsafeURLParamText(s string) bool {
	if strings.Contains(s, "://") || strings.Contains(s, "@") {
		return true
	}
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

func matchPathPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return false
	}
	rest := strings.TrimPrefix(path, prefix+"/")
	return rest != "" && !strings.HasSuffix(rest, "/")
}

func classifyPostRoute(u *url.URL) postURLRoute {
	path := u.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		return postRouteNone
	}
	switch u.Scheme {
	case "https":
		if u.Host != "line.me" {
			return postRouteNone
		}
		if matchPathPrefix(path, "/R/group/home/albums") || matchPathPrefix(path, "/R/home/albums") {
			return postRouteAlbum
		}
		if matchPathPrefix(path, "/R/group/home/posts") {
			return postRoutePost
		}
		if strings.HasPrefix(path, "/R/home/") && len(path) > len("/R/home/") && !strings.HasSuffix(path, "/") {
			return postRoutePost
		}
	case "line":
		switch u.Host {
		case "group":
			if matchPathPrefix(path, "/home/albums") || matchPathPrefix(path, "/albums") || matchPathPrefix(path, "/album") {
				return postRouteAlbum
			}
			if matchPathPrefix(path, "/home/posts") || matchPathPrefix(path, "/posts") || matchPathPrefix(path, "/post") {
				return postRoutePost
			}
		case "home":
			if matchPathPrefix(path, "/albums") || matchPathPrefix(path, "/album") {
				return postRouteAlbum
			}
			if matchPathPrefix(path, "/posts") || matchPathPrefix(path, "/post") || matchPathPrefix(path, "/public/post") {
				return postRoutePost
			}
		}
	}
	return postRouteNone
}

func isValidPostAlbumID(s string) bool {
	if s == "" || len(s) > maxPostIdentifierBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7E {
			return false
		}
	}
	return true
}

func isSafePreviewOID(oid string) bool {
	if oid == "" || len(oid) > maxPostIdentifierBytes || !utf8.ValidString(oid) {
		return false
	}
	if oid == "." || strings.Contains(oid, "..") || strings.ContainsAny(oid, "/\\") {
		return false
	}
	return !strings.ContainsFunc(oid, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

func parsePostPreviewMedias(raw string) ([]string, bool) {
	if len(raw) > maxContentBytes || !utf8.ValidString(raw) {
		return nil, false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, true
	}
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return nil, false
	}
	if len(items) > maxPostPreviewMedias {
		return nil, false
	}
	ok := true
	seen := make(map[string]bool, len(items))
	oids := make([]string, 0, len(items))
	for _, item := range items {
		itemTrimmed := strings.TrimSpace(string(item))
		if !strings.HasPrefix(itemTrimmed, "{") {
			ok = false
			continue
		}
		var entry struct {
			SVC       string `json:"svc"`
			SID       string `json:"sid"`
			MediaOID  string `json:"mediaOid"`
			MediaType string `json:"mediaType"`
		}
		if err := json.Unmarshal(item, &entry); err != nil {
			ok = false
			continue
		}
		if entry.SVC != "album" || entry.SID != "a" || entry.MediaType != "I" {
			continue
		}
		if !isSafePreviewOID(entry.MediaOID) {
			ok = false
			continue
		}
		if !seen[entry.MediaOID] {
			seen[entry.MediaOID] = true
			oids = append(oids, entry.MediaOID)
		}
	}
	return oids, ok
}
