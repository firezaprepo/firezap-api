package instance

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestDescribeDropsPlumbing(t *testing.T) {
	// The first half of a group message: only the sender-key distribution part.
	skdm := &waE2E.Message{
		SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{GroupID: proto.String("123@g.us")},
		MessageContextInfo:           &waE2E.MessageContextInfo{},
	}
	if d := Describe(skdm); d.Deliver {
		t.Fatalf("sender-key part delivered: %+v", d)
	}
	// The same, but carrying the text: delivered once, as text.
	withText := &waE2E.Message{
		SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{GroupID: proto.String("123@g.us")},
		Conversation:                 proto.String("oi"),
	}
	if d := Describe(withText); !d.Deliver || d.Kind != "text" || d.Text != "oi" {
		t.Fatalf("text with sender-key part: %+v", d)
	}
	// Key share / history-sync notice: dropped.
	keyShare := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE.Enum()}}
	if d := Describe(keyShare); d.Deliver {
		t.Fatalf("key share delivered: %+v", d)
	}
}

func TestDescribeRevokeAndEdit(t *testing.T) {
	revoke := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_REVOKE.Enum(),
		Key:  &waCommon.MessageKey{ID: proto.String("3EB0AAA")},
	}}
	if d := Describe(revoke); !d.Deliver || d.Kind != "revoke" || d.Target != "3EB0AAA" {
		t.Fatalf("revoke: %+v", d)
	}
	edit := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		Key:           &waCommon.MessageKey{ID: proto.String("3EB0BBB")},
		EditedMessage: &waE2E.Message{Conversation: proto.String("texto corrigido")},
	}}
	if d := Describe(edit); !d.Deliver || d.Kind != "edit" || d.Target != "3EB0BBB" || d.Text != "texto corrigido" {
		t.Fatalf("edit: %+v", d)
	}
}

func TestDescribeKeepsUnknownContent(t *testing.T) {
	// A poll is "other" for v0.1, but it is real content: delivered.
	poll := &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Almoço?")}}
	if d := Describe(poll); !d.Deliver || d.Kind != "other" {
		t.Fatalf("poll: %+v", d)
	}
}

type fakeLIDs map[string]types.JID

func (f fakeLIDs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if pn, ok := f[lid.String()]; ok {
		return pn, nil
	}
	return types.JID{}, errors.New("not found")
}

func TestPhoneJID(t *testing.T) {
	ctx := context.Background()
	pn := types.NewJID("5511987654321", types.DefaultUserServer)
	lid := types.NewJID("164012345678994", types.HiddenUserServer)
	other := types.NewJID("222012345678940", types.HiddenUserServer)
	lids := fakeLIDs{lid.String(): pn}

	if got := phoneJID(ctx, lids, pn, types.JID{}); got != "5511987654321@s.whatsapp.net" {
		t.Errorf("phone JID: %q", got)
	}
	// Alternate address sent with the message wins over the lookup.
	if got := phoneJID(ctx, nil, other, pn); got != "5511987654321@s.whatsapp.net" {
		t.Errorf("alt: %q", got)
	}
	if got := phoneJID(ctx, lids, lid, types.JID{}); got != "5511987654321@s.whatsapp.net" {
		t.Errorf("lookup: %q", got)
	}
	if got := phoneJID(ctx, lids, other, types.JID{}); got != "" {
		t.Errorf("unknown LID: %q", got)
	}
	// A device suffix (":12") is dropped.
	dev := types.JID{User: "5511987654321", Server: types.DefaultUserServer, Device: 12}
	if got := phoneJID(ctx, nil, dev, types.JID{}); got != "5511987654321@s.whatsapp.net" {
		t.Errorf("device: %q", got)
	}
}

func TestChatType(t *testing.T) {
	cases := map[string]string{
		"5511987654321@s.whatsapp.net":  "private",
		"164012345678994@lid":           "private",
		"120363000000000029@g.us":       "group",
		"status@broadcast":              "status",
		"1234567890@broadcast":          "broadcast",
		"120363000000000002@newsletter": "newsletter",
	}
	for raw, want := range cases {
		jid, err := types.ParseJID(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := ChatType(jid); got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
}
