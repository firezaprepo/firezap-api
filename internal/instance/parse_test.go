package instance

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestParseRecipient(t *testing.T) {
	ok := map[string]string{
		"5511999999999":       "5511999999999@s.whatsapp.net",
		"+55 (11) 99999-9999": "5511999999999@s.whatsapp.net",
		"120363012345@g.us":   "120363012345@g.us",
	}
	for in, want := range ok {
		jid, err := ParseRecipient(in)
		if err != nil {
			t.Errorf("ParseRecipient(%q) error: %v", in, err)
			continue
		}
		if jid.String() != want {
			t.Errorf("ParseRecipient(%q) = %s, want %s", in, jid, want)
		}
	}
	for _, bad := range []string{"", "1234", "55119999abc", "1234567890123456", "@g.us"} {
		if _, err := ParseRecipient(bad); err == nil {
			t.Errorf("ParseRecipient(%q) should fail", bad)
		}
	}
}

func TestMessageContent(t *testing.T) {
	cases := []struct {
		msg        *waE2E.Message
		kind, text string
	}{
		{&waE2E.Message{Conversation: proto.String("hi")}, "text", "hi"},
		{&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link")}}, "text", "link"},
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("photo")}}, "image", "photo"},
		{&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, "audio", ""},
		{nil, "other", ""},
	}
	for _, c := range cases {
		kind, text := MessageContent(c.msg)
		if kind != c.kind || text != c.text {
			t.Errorf("MessageContent = (%q, %q), want (%q, %q)", kind, text, c.kind, c.text)
		}
	}
}
