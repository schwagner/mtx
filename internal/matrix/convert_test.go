package matrix

import (
	"strings"
	"testing"

	"go.mau.fi/gomuks/pkg/hicli/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestAttachmentsForImage(t *testing.T) {
	evt := &database.Event{RoomID: "!room:x", ID: "$ab/cd+ef"}
	content := &event.MessageEventContent{
		MsgType: event.MsgImage,
		Body:    "cat.png",
		URL:     "mxc://x/full",
		Info: &event.FileInfo{
			MimeType: "image/png", Width: 1600, Height: 1200, Size: 4000,
			ThumbnailURL:  "mxc://x/thumb",
			ThumbnailInfo: &event.FileInfo{Width: 800, Height: 600},
		},
	}
	atts := attachmentsFor(evt, content)
	if len(atts) != 1 {
		t.Fatalf("got %d attachments, want 1", len(atts))
	}
	a := atts[0]
	if a.Kind != "image" || a.Mime != "image/png" || a.Name != "cat.png" || a.Size != 4000 {
		t.Errorf("got %+v", a)
	}
	// FileID names files in the image cache, so it must be a safe
	// file name whatever characters the event ID has.
	if a.FileID == "" || strings.ContainsAny(a.FileID, "/$+:!") {
		t.Errorf("FileID %q is not a safe file name", a.FileID)
	}
	ref := eventRef(evt.RoomID, evt.ID)
	if a.DownloadURL != ref {
		t.Errorf("DownloadURL = %q, want %q", a.DownloadURL, ref)
	}
	// Ascending by size: the renderer takes the last as the largest.
	if len(a.Thumbs) != 2 ||
		a.Thumbs[0].URL != ref+thumbnailSuffix || a.Thumbs[0].W != 800 || a.Thumbs[0].H != 600 ||
		a.Thumbs[1].URL != ref || a.Thumbs[1].W != 1600 || a.Thumbs[1].H != 1200 {
		t.Errorf("Thumbs = %+v", a.Thumbs)
	}
}

// Without dimensions there is no layout to reserve, so the image gets
// no thumbs and the TUI shows its one-line [Image] link instead.
func TestAttachmentsForImageWithoutSize(t *testing.T) {
	evt := &database.Event{RoomID: "!room:x", ID: "$e"}
	content := &event.MessageEventContent{
		MsgType: event.MsgImage, Body: "cat.png", URL: "mxc://x/full",
		Info: &event.FileInfo{
			ThumbnailURL:  "mxc://x/thumb",
			ThumbnailInfo: &event.FileInfo{},
		},
	}
	a := attachmentsFor(evt, content)[0]
	if a.Kind != "image" || len(a.Thumbs) != 0 {
		t.Errorf("got %+v, want an image with no thumbs", a)
	}
}

func TestAttachmentsForFile(t *testing.T) {
	evt := &database.Event{RoomID: "!room:x", ID: "$e"}
	content := &event.MessageEventContent{MsgType: event.MsgFile, Body: "a.pdf", URL: "mxc://x/f"}
	a := attachmentsFor(evt, content)[0]
	if a.Kind != "file" || a.FileID != "" || len(a.Thumbs) != 0 {
		t.Errorf("got %+v, want a plain file", a)
	}
}

func TestMediaSource(t *testing.T) {
	encThumb := &event.EncryptedFileInfo{URL: "mxc://x/encthumb"}
	encFull := &event.EncryptedFileInfo{URL: "mxc://x/encfull"}
	cases := []struct {
		name    string
		content *event.MessageEventContent
		thumb   bool
		wantURL id.ContentURIString
		wantEnc *event.EncryptedFileInfo
		wantMT  string
	}{
		{"plain full", &event.MessageEventContent{URL: "mxc://x/full", Info: &event.FileInfo{MimeType: "image/png"}},
			false, "mxc://x/full", nil, "image/png"},
		{"encrypted full", &event.MessageEventContent{File: encFull, Info: &event.FileInfo{MimeType: "image/jpeg"}},
			false, "mxc://x/encfull", encFull, "image/jpeg"},
		{"plain thumb", &event.MessageEventContent{URL: "mxc://x/full", Info: &event.FileInfo{
			ThumbnailURL: "mxc://x/thumb", ThumbnailInfo: &event.FileInfo{MimeType: "image/webp"}}},
			true, "mxc://x/thumb", nil, "image/webp"},
		{"encrypted thumb", &event.MessageEventContent{File: encFull, Info: &event.FileInfo{ThumbnailFile: encThumb}},
			true, "mxc://x/encthumb", encThumb, ""},
		{"thumb missing falls back to full", &event.MessageEventContent{URL: "mxc://x/full", Info: &event.FileInfo{MimeType: "image/png"}},
			true, "mxc://x/full", nil, "image/png"},
	}
	for _, c := range cases {
		url, enc, mt := mediaSource(c.content, c.thumb)
		if url != c.wantURL || enc != c.wantEnc || mt != c.wantMT {
			t.Errorf("%s: got (%q, %v, %q), want (%q, %v, %q)", c.name, url, enc, mt, c.wantURL, c.wantEnc, c.wantMT)
		}
	}
}

func TestParseMediaRef(t *testing.T) {
	ref := eventRef("!room:x", "$ab/cd")
	room, evt, thumb, ok := parseMediaRef(ref + thumbnailSuffix)
	if !ok || room != "!room:x" || evt != "$ab/cd" || !thumb {
		t.Errorf("thumbnail ref: got (%q, %q, %v, %v)", room, evt, thumb, ok)
	}
	room, evt, thumb, ok = parseMediaRef(ref)
	if !ok || room != "!room:x" || evt != "$ab/cd" || thumb {
		t.Errorf("full ref: got (%q, %q, %v, %v)", room, evt, thumb, ok)
	}
	if _, _, _, ok := parseMediaRef("https://example.com/a.png"); ok {
		t.Error("an http URL parsed as a media ref")
	}
}
