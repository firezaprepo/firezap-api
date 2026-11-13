package instance

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestQuoteContext(t *testing.T) {
	private := types.NewJID("5511987654321", types.DefaultUserServer)
	group := types.NewJID("120363000000000000", types.GroupServer)

	// Someone else's message in a private chat: the participant is the other person.
	ci, err := quoteContext(nil, private, &Quote{MessageID: "ABC123", Text: "original"})
	if err != nil || ci.GetStanzaID() != "ABC123" || ci.GetParticipant() != private.String() ||
		ci.GetQuotedMessage().GetConversation() != "original" {
		t.Fatalf("private: %+v %v", ci, err)
	}
	// Own message: the participant is this number, in the chat's addressing.
	own := types.NewJID("5511900000000", types.DefaultUserServer)
	cli := &whatsmeow.Client{Store: &wastore.Device{ID: &own, LID: types.NewJID("99999", types.HiddenUserServer)}}
	if ci, err = quoteContext(cli, private, &Quote{MessageID: "ABC123", FromMe: true}); err != nil || ci.GetParticipant() != own.String() {
		t.Fatalf("fromMe: %+v %v", ci, err)
	}
	lidChat := types.NewJID("12345", types.HiddenUserServer)
	if ci, err = quoteContext(cli, lidChat, &Quote{MessageID: "ABC123", FromMe: true}); err != nil || ci.GetParticipant() != "99999@lid" {
		t.Fatalf("fromMe in a LID chat: %+v %v", ci, err)
	}
	// Group without participant, and a bad id: refused.
	if _, err = quoteContext(nil, group, &Quote{MessageID: "ABC123"}); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("group without participant: %v", err)
	}
	if _, err = quoteContext(nil, private, &Quote{MessageID: ""}); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("empty id: %v", err)
	}
	// No quote: no context.
	if ci, err = quoteContext(nil, private, nil); ci != nil || err != nil {
		t.Fatalf("nil quote: %+v %v", ci, err)
	}
}

func TestWithContext(t *testing.T) {
	ci := &waE2E.ContextInfo{StanzaID: proto.String("ABC123")}
	// Plain text becomes extended text, keeping the text.
	m := withContext(&waE2E.Message{Conversation: proto.String("oi")}, ci)
	if m.GetConversation() != "" || m.GetExtendedTextMessage().GetText() != "oi" ||
		m.GetExtendedTextMessage().GetContextInfo().GetStanzaID() != "ABC123" {
		t.Fatalf("text reply: %+v", m)
	}
	img := withContext(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, ci)
	if img.GetImageMessage().GetContextInfo().GetStanzaID() != "ABC123" {
		t.Fatalf("image reply: %+v", img)
	}
	plain := &waE2E.Message{Conversation: proto.String("oi")}
	if withContext(plain, nil) != plain {
		t.Fatal("without a quote the message must be unchanged")
	}
}

func TestQuotedOf(t *testing.T) {
	reply := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String("resposta"),
		ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("ABC123"), Participant: proto.String("123@lid")},
	}}
	if q := quotedOf(reply); q == nil || q.ID != "ABC123" || q.Participant != "123@lid" {
		t.Fatalf("reply: %+v", q)
	}
	if q := quotedOf(&waE2E.Message{Conversation: proto.String("oi")}); q != nil {
		t.Fatalf("plain text is not a reply: %+v", q)
	}
	// Context without a quoted message (for example a forwarded message): not a reply.
	fwd := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true)}}}
	if q := quotedOf(fwd); q != nil {
		t.Fatalf("forwarded is not a reply: %+v", q)
	}
}

func TestEditAndDeleteValidation(t *testing.T) {
	m, ctx := &Manager{}, context.Background() // validation fails before any connection is needed
	if _, err := m.EditText(ctx, "x", "5511987654321", "ABC123", "  "); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("empty edit: %v", err)
	}
	if _, err := m.EditText(ctx, "x", "5511987654321", "", "novo texto"); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("edit without id: %v", err)
	}
	// Someone else's message in a private chat cannot be deleted for everyone.
	if _, err := m.DeleteMessage(ctx, "x", MessageRef{Chat: "5511987654321", MessageID: "ABC123"}); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("delete other's private message: %v", err)
	}
	if _, err := m.DeleteMessage(ctx, "x", MessageRef{Chat: "120363000000000000@g.us", MessageID: "ABC123"}); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("delete in group without participant: %v", err)
	}
}
