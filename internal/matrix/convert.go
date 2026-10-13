package matrix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/emoji"
	"github.com/schwagner/mtx/internal/ui/messages"
)

// effectiveType is the event type after decryption.
func effectiveType(evt *database.Event) string {
	if evt.DecryptedType != "" {
		return evt.DecryptedType
	}
	return evt.Type
}

func effectiveContent(evt *database.Event) json.RawMessage {
	if evt.Decrypted != nil {
		return evt.Decrypted
	}
	return evt.Content
}

func parseMessage(evt *database.Event) *event.MessageEventContent {
	var content event.MessageEventContent
	if err := json.Unmarshal(effectiveContent(evt), &content); err != nil {
		return nil
	}
	return &content
}

// isTimelineMessage reports whether evt is shown as its own row. Edits,
// reactions and redactions modify other rows; state events and unknown
// types are not shown.
func isTimelineMessage(evt *database.Event) bool {
	if evt.StateKey != nil {
		return false
	}
	switch effectiveType(evt) {
	case event.EventMessage.Type, event.EventSticker.Type:
		return evt.RelationType != event.RelReplace
	case event.EventEncrypted.Type:
		// Still encrypted: shown as an undecryptable placeholder until
		// keys arrive (EventsDecrypted then re-renders it in place).
		return evt.RelationType != event.RelReplace
	}
	return false
}

// threadRoot returns the root event ID if evt is a thread reply.
func threadRoot(evt *database.Event) id.EventID {
	if evt.RelationType == event.RelThread {
		return evt.RelatesTo
	}
	return ""
}

// slackEscape escapes the characters the TUI's Slack-markup renderer treats
// as markup delimiters; it decodes them again after rendering.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// htmlToSlack converts Matrix HTML (formatted_body) to the Slack mrkdwn
// subset the TUI renders: *bold*, _italic_, ~strike~, `code`, ```blocks```,
// > quotes and <url|label> links. names resolves pills to display names.
func htmlToSlack(html string, names func(id.UserID) string) string {
	parser := &format.HTMLParser{
		TabsToSpaces: 4,
		Newline:      "\n",
		BoldConverter: func(s string, _ format.Context) string {
			return wrapInline(s, "*")
		},
		ItalicConverter: func(s string, _ format.Context) string {
			return wrapInline(s, "_")
		},
		StrikethroughConverter: func(s string, _ format.Context) string {
			return wrapInline(s, "~")
		},
		MonospaceConverter: func(s string, _ format.Context) string {
			return "`" + s + "`"
		},
		MonospaceBlockConverter: func(code, _ string, _ format.Context) string {
			return "```\n" + strings.TrimRight(code, "\n") + "\n```"
		},
		LinkConverter: func(text, href string, _ format.Context) string {
			if text == href || text == "" {
				return "<" + href + ">"
			}
			return "<" + href + "|" + text + ">"
		},
		PillConverter: func(displayname, mxid, eventID string, _ format.Context) string {
			if strings.HasPrefix(mxid, "@") {
				name := names(id.UserID(mxid))
				if name == "" {
					name = displayname
				}
				return "*@" + strings.TrimPrefix(name, "@") + "*"
			}
			if displayname != "" {
				return displayname
			}
			return mxid
		},
		SpoilerConverter: func(text, reason string, _ format.Context) string {
			if reason != "" {
				return "[spoiler: " + reason + "]"
			}
			return "[spoiler]"
		},
		TextConverter: func(s string, _ format.Context) string {
			return slackEscaper.Replace(s)
		},
	}
	return strings.TrimSpace(parser.Parse(html, format.NewContext(nil)))
}

// wrapInline applies a Slack inline marker per line: the renderer's
// regexes don't span newlines.
func wrapInline(s, marker string) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = marker + l + marker
		}
	}
	return strings.Join(lines, "\n")
}

// plainToSlack prepares an unformatted body for the Slack-markup renderer.
// Plain bodies are typically already markdown-ish, so only bold (**x**)
// needs translating; everything else is escaped.
var mdBoldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)

func plainToSlack(body string) string {
	body = slackEscaper.Replace(body)
	return mdBoldRe.ReplaceAllString(body, "*$1*")
}

// messageText renders a message event's content as TUI text.
func messageText(content *event.MessageEventContent, senderName string, names func(id.UserID) string) string {
	if content.NewContent != nil {
		content = content.NewContent
	}
	content.RemoveReplyFallback()
	var text string
	if content.Format == event.FormatHTML && content.FormattedBody != "" {
		text = htmlToSlack(content.FormattedBody, names)
	} else {
		text = plainToSlack(content.Body)
	}
	switch content.MsgType {
	case event.MsgEmote:
		text = "_* " + slackEscaper.Replace(senderName) + "_ " + text
	case event.MsgImage, event.MsgFile, event.MsgVideo, event.MsgAudio:
		// The filename is shown by the attachment row; a caption is
		// only present when body differs from the filename.
		if content.FileName == "" || content.Body == content.FileName {
			text = ""
		}
	}
	return text
}

func attachmentsFor(evt *database.Event, content *event.MessageEventContent) []messages.Attachment {
	switch content.MsgType {
	case event.MsgImage, event.MsgFile, event.MsgVideo, event.MsgAudio:
	default:
		return nil
	}
	name := content.FileName
	if name == "" {
		name = content.Body
	}
	var size int64
	if content.Info != nil {
		size = int64(content.Info.Size)
	}
	// Downloads go through FileService.Download, which resolves this
	// reference back to the event so it can fetch and decrypt the media.
	ref := eventRef(evt.RoomID, evt.ID)
	att := messages.Attachment{
		Kind:        "file",
		Name:        name,
		URL:         ref,
		DownloadURL: ref,
		Size:        size,
	}
	if content.MsgType == event.MsgImage {
		att.Kind = "image"
		att.FileID = imageFileID(evt.ID)
		att.Thumbs = imageThumbs(ref, content.Info)
		if content.Info != nil {
			att.Mime = content.Info.MimeType
		}
	}
	return []messages.Attachment{att}
}

// imageFileID keys an image in the TUI's image cache, which uses it as a
// file name, so it is a hash of the event ID rather than the ID itself
// (event IDs may contain '/').
func imageFileID(evtID id.EventID) string {
	sum := sha256.Sum256([]byte(evtID))
	return "mx-" + hex.EncodeToString(sum[:12])
}

// imageThumbs lists the sizes an image can be fetched at, ascending:
// the event's own thumbnail, then the full image. The URLs are media
// refs for Backend.FetchImage. A size the event does not state is left
// out; with none at all the TUI cannot lay the image out and shows a
// link line instead.
func imageThumbs(ref string, info *event.FileInfo) []messages.ThumbSpec {
	if info == nil || info.Width <= 0 || info.Height <= 0 {
		return nil
	}
	var thumbs []messages.ThumbSpec
	if ti := info.ThumbnailInfo; ti != nil && ti.Width > 0 && ti.Height > 0 &&
		(info.ThumbnailURL != "" || info.ThumbnailFile != nil) &&
		max(ti.Width, ti.Height) < max(info.Width, info.Height) {
		thumbs = append(thumbs, messages.ThumbSpec{URL: ref + thumbnailSuffix, W: ti.Width, H: ti.Height})
	}
	return append(thumbs, messages.ThumbSpec{URL: ref, W: info.Width, H: info.Height})
}

const eventRefPrefix = "mtx-event:"

func eventRef(room id.RoomID, evtID id.EventID) string {
	return eventRefPrefix + string(room) + "/" + string(evtID)
}

// thumbnailSuffix on an event ref names the event's thumbnail rather
// than its full media.
const thumbnailSuffix = "#thumbnail"

// parseMediaRef parses an image URL from imageThumbs.
func parseMediaRef(ref string) (room id.RoomID, evt id.EventID, thumb bool, ok bool) {
	ref, thumb = strings.CutSuffix(ref, thumbnailSuffix)
	room, evt, ok = parseEventRef(ref)
	return room, evt, thumb, ok
}

func parseEventRef(ref string) (id.RoomID, id.EventID, bool) {
	rest, ok := strings.CutPrefix(ref, eventRefPrefix)
	if !ok {
		return "", "", false
	}
	room, evt, ok := strings.Cut(rest, "/")
	return id.RoomID(room), id.EventID(evt), ok
}

// reactionName maps a Matrix reaction key (usually a Unicode emoji) to the
// shortcode name the TUI's reaction rows and picker use. Keys without a
// shortcode are passed through unchanged.
func reactionName(key string) string {
	if name, ok := glyphToName()[key]; ok {
		return name
	}
	if name, ok := glyphToName()[strings.TrimSuffix(key, "️")]; ok {
		return name
	}
	return key
}

// reactionKey is the inverse of reactionName for outgoing reactions.
func reactionKey(name string) string {
	if glyph, ok := emoji.CodeMap()[":"+name+":"]; ok {
		return glyph
	}
	return name
}

// glyphToName maps each emoji glyph to its shortest shortcode name.
var glyphToName = sync.OnceValue(func() map[string]string {
	m := make(map[string]string, len(emoji.CodeMap()))
	for code, glyph := range emoji.CodeMap() {
		name := strings.Trim(code, ":")
		// Several aliases share a glyph; keep the shortest (then
		// alphabetically first) so the choice is deterministic.
		if cur, ok := m[glyph]; !ok || len(name) < len(cur) || (len(name) == len(cur) && name < cur) {
			m[glyph] = name
		}
	}
	return m
})

func formatTime(t time.Time, layout string) (clock, date string) {
	if layout == "" {
		layout = "3:04 PM"
	}
	local := t.Local()
	return local.Format(layout), local.Format("2006-01-02")
}
