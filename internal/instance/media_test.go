package instance

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestMediaOfAndJSONRoundTrip(t *testing.T) {
	msg := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype: proto.String("audio/ogg; codecs=opus"), FileLength: proto.Uint64(5120), Seconds: proto.Uint32(7), PTT: proto.Bool(true),
		DirectPath: proto.String("/v/t62.7117-24/abc"), MediaKey: key(1), FileSHA256: key(2), FileEncSHA256: key(3),
	}}
	d := Describe(msg)
	if !d.Deliver || d.Kind != "audio" || d.Media == nil {
		t.Fatalf("describe: %+v", d)
	}
	if m := d.Media; m.Kind != "audio" || !m.PTT || m.Seconds != 7 || m.Size != 5120 {
		t.Fatalf("media: %+v", m)
	}
	// The integration receives the object in the webhook and sends it back unchanged.
	raw, err := json.Marshal(d.Media)
	if err != nil {
		t.Fatal(err)
	}
	var back Media
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if _, err := downloadable(back); err != nil {
		t.Fatalf("round trip not downloadable: %v (%s)", err, raw)
	}
	if !bytes.Equal(back.MediaKey, key(1)) {
		t.Fatal("media key changed in the round trip")
	}
	// Text has no attachment.
	if MediaOf(&waE2E.Message{Conversation: proto.String("oi")}) != nil {
		t.Fatal("text message with media")
	}
}

func TestDownloadableRejectsBadInput(t *testing.T) {
	good := Media{Kind: "image", DirectPath: "/v/x", MediaKey: key(1), FileSHA256: key(2), FileEncSHA256: key(3)}
	if _, err := downloadable(good); err != nil {
		t.Fatalf("good: %v", err)
	}
	cases := map[string]func(m *Media){
		"no slash":      func(m *Media) { m.DirectPath = "v/x" },
		"absolute url":  func(m *Media) { m.DirectPath = "https://evil.example/x" },
		"space":         func(m *Media) { m.DirectPath = "/v/x y" },
		"short key":     func(m *Media) { m.MediaKey = key(1)[:16] },
		"missing hash":  func(m *Media) { m.FileSHA256 = nil },
		"unknown kind":  func(m *Media) { m.Kind = "pdf" },
		"empty kind":    func(m *Media) { m.Kind = "" },
		"missing enc":   func(m *Media) { m.FileEncSHA256 = nil },
		"backslash":     func(m *Media) { m.DirectPath = `/v\x` },
		"fragment hack": func(m *Media) { m.DirectPath = "/v#x" },
	}
	for name, mut := range cases {
		m := good
		mut(&m)
		if _, err := downloadable(m); !errors.Is(err, ErrInvalidMedia) {
			t.Errorf("%s: got %v, want ErrInvalidMedia", name, err)
		}
	}
	if msg, _ := downloadable(Media{Kind: "sticker", DirectPath: "/s", MediaKey: key(1), FileSHA256: key(2), FileEncSHA256: key(3)}); whatsmeow.GetMediaType(msg) == "" {
		t.Fatal("sticker has no media type")
	}
}

func TestPrepareOutgoing(t *testing.T) {
	data := []byte("x")
	ok := []OutgoingMedia{
		{Kind: "image", Data: data, Mimetype: "image/jpeg"},
		{Kind: "image", Data: data, Mimetype: "image/png"},
		{Kind: "video", Data: data, Mimetype: "video/mp4"},
		{Kind: "audio", Data: data, Mimetype: "audio/mpeg"},
		{Kind: "audio", Data: data, Mimetype: "application/ogg", PTT: true},
		{Kind: "document", Data: data, Mimetype: "application/pdf", FileName: "boleto.pdf"},
		{Kind: "document", Data: data},
	}
	for _, o := range ok {
		o := o
		if err := prepareOutgoing(&o); err != nil {
			t.Errorf("%s %s: %v", o.Kind, o.Mimetype, err)
		}
	}
	bad := []OutgoingMedia{
		{Kind: "image", Data: data, Mimetype: "image/webp"},
		{Kind: "image", Data: data, Mimetype: "application/pdf"},
		{Kind: "video", Data: data, Mimetype: "video/quicktime"},
		{Kind: "audio", Data: data, Mimetype: "audio/mpeg", PTT: true}, // voice notes need Opus
		{Kind: "audio", Data: data, Mimetype: "text/plain"},
		{Kind: "sticker", Data: data, Mimetype: "image/webp"},
		{Kind: "image", Data: nil, Mimetype: "image/jpeg"},
	}
	for _, o := range bad {
		o := o
		if err := prepareOutgoing(&o); !errors.Is(err, ErrInvalidMedia) {
			t.Errorf("%s %s ptt=%v: got %v, want ErrInvalidMedia", o.Kind, o.Mimetype, o.PTT, err)
		}
	}
	// Normalization.
	voice := OutgoingMedia{Kind: "audio", Data: data, Mimetype: "audio/ogg", PTT: true}
	_ = prepareOutgoing(&voice)
	if voice.Mimetype != "audio/ogg; codecs=opus" {
		t.Errorf("voice mimetype: %q", voice.Mimetype)
	}
	doc := OutgoingMedia{Kind: "document", Data: data}
	_ = prepareOutgoing(&doc)
	if doc.Mimetype != "application/octet-stream" || doc.FileName != "file" {
		t.Errorf("document defaults: %+v", doc)
	}
}

func TestBuildMediaMessage(t *testing.T) {
	up := whatsmeow.UploadResponse{URL: "https://mmg/x", DirectPath: "/x", MediaKey: key(1), FileSHA256: key(2), FileEncSHA256: key(3), FileLength: 9}
	img := buildMediaMessage(OutgoingMedia{Kind: "image", Mimetype: "image/png", Caption: "foto"}, up)
	if img.GetImageMessage().GetCaption() != "foto" || img.GetImageMessage().GetFileLength() != 9 {
		t.Fatalf("image: %v", img)
	}
	doc := buildMediaMessage(OutgoingMedia{Kind: "document", Mimetype: "application/pdf", FileName: "a.pdf"}, up)
	if doc.GetDocumentMessage().GetFileName() != "a.pdf" || doc.GetDocumentMessage().Caption != nil {
		t.Fatalf("document: %v", doc)
	}
	voice := buildMediaMessage(OutgoingMedia{Kind: "audio", Mimetype: "audio/ogg; codecs=opus", PTT: true}, up)
	if !voice.GetAudioMessage().GetPTT() {
		t.Fatalf("voice: %v", voice)
	}
	// What we build is what we read back on the other side.
	if k, _ := MessageContent(img); k != "image" {
		t.Fatalf("kind %q", k)
	}
}

func TestCappedFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cap")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := cappedFile{File: f, max: 100}
	if _, err := c.Write(make([]byte, 100+encryptionOverhead)); err != nil {
		t.Fatalf("write within limit: %v", err)
	}
	if _, err := c.Write([]byte{1}); !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("write past limit: %v", err)
	}
	if _, err := c.WriteAt([]byte{1}, 100+encryptionOverhead); !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("writeAt past limit: %v", err)
	}
	if err := c.Truncate(1 << 30); !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("truncate past limit: %v", err)
	}
}
