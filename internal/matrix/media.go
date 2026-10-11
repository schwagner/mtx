package matrix

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ids"
	"github.com/schwagner/mtx/internal/text"
	"github.com/schwagner/mtx/internal/ui"
)

// searchChannel runs a local full-text search over the room's (decrypted)
// messages. Encrypted rooms can't be searched server-side, so local is
// the only option that covers them.
func (b *Backend) searchChannel(channelID ids.ChannelID, query string) core.Msg {
	ctx := b.ctx()
	roomID := id.RoomID(channelID)
	terms := strings.Fields(query)
	folded := make([]string, 0, len(terms))
	quoted := make([]string, 0, len(terms))
	for _, t := range terms {
		folded = append(folded, text.Fold(t))
		// Quote each term so FTS5 operators in user input are literal.
		quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"*`)
	}
	resp, err := b.cli.SearchLocal(ctx, &jsoncmd.SearchParams{
		SearchTerm: strings.Join(quoted, " "),
		RoomIDs:    []id.RoomID{roomID},
		Limit:      200,
		SortByTime: true,
	})
	msg := ui.ChannelSearchResultsMsg{ChannelID: string(channelID), Query: query, Terms: folded, Err: err}
	if err != nil {
		return msg
	}
	for _, evt := range resp.Events {
		if isTimelineMessage(evt) && threadRoot(evt) == "" {
			msg.TSes = append(msg.TSes, b.idx.put(evt))
		}
	}
	return msg
}

// upload sends each attachment as its own file message, encrypting it
// first when the room is encrypted. A caption follows as a text message.
func (b *Backend) upload(channelID, threadTS, caption string, attachments []core.PendingAttachment) core.Cmd {
	return func() core.Msg {
		ctx := b.ctx()
		roomID := id.RoomID(channelID)
		room := b.room(roomID)
		if room == nil {
			return ui.UploadResultMsg{Err: errors.New("unknown room")}
		}
		var rel *event.RelatesTo
		if threadTS != "" {
			root, replies, err := b.threadEvents(ctx, roomID, threadTS)
			if err != nil {
				return ui.UploadResultMsg{Err: err}
			}
			latest := root.ID
			if n := len(replies); n > 0 {
				latest = replies[n-1].ID
			}
			rel = (&event.RelatesTo{}).SetThread(root.ID, latest)
		}
		for i, att := range attachments {
			b.emit(ui.UploadProgressMsg{Done: i, Total: len(attachments)})
			data := att.Bytes
			if data == nil {
				var err error
				data, err = os.ReadFile(att.Path)
				if err != nil {
					return ui.UploadResultMsg{Err: fmt.Errorf("reading %s: %w", att.Filename, err)}
				}
			}
			if err := b.sendFile(ctx, room, att.Filename, att.Mime, data, rel); err != nil {
				return ui.UploadResultMsg{Err: fmt.Errorf("uploading %s: %w", att.Filename, err)}
			}
		}
		if strings.TrimSpace(caption) != "" {
			body, mentions := b.prepareText(caption)
			if _, err := b.cli.SendMessage(ctx, roomID, nil, nil, body, rel, mentions, nil); err != nil {
				return ui.UploadResultMsg{Err: err}
			}
		}
		b.emit(ui.UploadProgressMsg{Done: len(attachments), Total: len(attachments)})
		return ui.UploadResultMsg{}
	}
}

func (b *Backend) sendFile(ctx context.Context, room *database.Room, name, mimeType string, data []byte, rel *event.RelatesTo) error {
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	content := &event.MessageEventContent{
		MsgType:  event.MsgFile,
		Body:     name,
		FileName: name,
		Info:     &event.FileInfo{MimeType: mimeType, Size: len(data)},
	}
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		content.MsgType = event.MsgImage
	case strings.HasPrefix(mimeType, "video/"):
		content.MsgType = event.MsgVideo
	case strings.HasPrefix(mimeType, "audio/"):
		content.MsgType = event.MsgAudio
	}
	uploadMime := mimeType
	var file *attachment.EncryptedFile
	if room.EncryptionEvent != nil {
		file = attachment.NewEncryptedFile()
		file.EncryptInPlace(data)
		uploadMime = "application/octet-stream"
	}
	resp, err := b.cli.Client.UploadBytes(ctx, data, uploadMime)
	if err != nil {
		return err
	}
	if file != nil {
		content.File = &event.EncryptedFileInfo{EncryptedFile: *file, URL: resp.ContentURI.CUString()}
	} else {
		content.URL = resp.ContentURI.CUString()
	}
	content.RelatesTo = rel
	_, err = b.cli.Send(ctx, room.ID, event.EventMessage, content, false, true)
	return err
}

// download fetches (and decrypts) the media of the event ref points at
// into the download directory, returning the saved path.
func (b *Backend) download(ctx context.Context, ref, name string) (string, error) {
	roomID, evtID, ok := parseEventRef(ref)
	if !ok {
		return "", fmt.Errorf("not a Matrix attachment: %s", ref)
	}
	evt, err := b.cli.DB.Event.GetByID(ctx, roomID, evtID)
	if err != nil || evt == nil {
		return "", fmt.Errorf("attachment event not found")
	}
	content := parseMessage(evt)
	if content == nil {
		return "", fmt.Errorf("unreadable attachment event")
	}
	var mxc id.ContentURIString
	if content.File != nil {
		mxc = content.File.URL
	} else {
		mxc = content.URL
	}
	uri, err := mxc.Parse()
	if err != nil {
		return "", fmt.Errorf("bad media URL: %w", err)
	}
	data, err := b.cli.Client.DownloadBytes(ctx, uri)
	if err != nil {
		return "", err
	}
	if content.File != nil {
		if err := content.File.DecryptInPlace(data); err != nil {
			return "", fmt.Errorf("decrypting: %w", err)
		}
	}
	dir := b.DownloadDir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := uniquePath(filepath.Join(dir, filepath.Base(name)))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// uniquePath appends " (n)" before the extension until path is unused.
func uniquePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}
