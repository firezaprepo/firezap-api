package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// ErrInvalidMessageRef is returned when a request points to a message in a way that cannot work.
var ErrInvalidMessageRef = errors.New("invalid message reference")

// maxEditLength is the same limit as a text message.
const maxEditLength = 4096

// MessageRef points to an existing message with the values of its message.received event.
// The API keeps no messages, so the caller says who sent it.
type MessageRef struct {
	Chat        string // "chat" from message.received
	MessageID   string // "id" from message.received
	FromMe      bool   // true when the message was sent by this number
	Participant string // groups only, when FromMe is false: "sender" from message.received
}

// validMessageID accepts WhatsApp message ids (letters, digits, "-", "_" and ".", at most 128).
func validMessageID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// resolveRef returns the chat of the message and who sent it. The sender is empty for this
// number's own messages: whatsmeow then marks the message key as "from me".
func resolveRef(r MessageRef) (chat, sender types.JID, err error) {
	if chat, err = ParseRecipient(r.Chat); err != nil {
		return chat, sender, err
	}
	sender, err = senderIn(chat, r.MessageID, r.FromMe, r.Participant)
	return chat, sender, err
}

// senderIn works out the sender of a message in a known chat.
func senderIn(chat types.JID, messageID string, fromMe bool, participant string) (types.JID, error) {
	if !validMessageID(messageID) {
		return types.EmptyJID, fmt.Errorf("%w: messageId is missing or invalid", ErrInvalidMessageRef)
	}
	switch {
	case fromMe:
		return types.EmptyJID, nil
	case chat.Server == types.GroupServer:
		if strings.TrimSpace(participant) == "" {
			return types.EmptyJID, fmt.Errorf("%w: participant is required for someone else's message in a group", ErrInvalidMessageRef)
		}
		return ParseRecipient(participant)
	default:
		// In a private chat, a message not sent by this number was sent by the other person.
		return chat, nil
	}
}

// Quote makes a new message a reply to an earlier message in the same chat.
type Quote struct {
	MessageID   string // "id" of the message being answered
	FromMe      bool   // true when that message was sent by this number
	Participant string // groups only, when FromMe is false: its "sender"
	// Text is shown inside the reply bubble. The API keeps no messages, so it cannot fill it
	// in by itself; without it the phone shows the reply with an empty quote.
	Text string
}

// quoteContext builds the reply information WhatsApp attaches to a message.
func quoteContext(cli *whatsmeow.Client, chat types.JID, q *Quote) (*waE2E.ContextInfo, error) {
	if q == nil {
		return nil, nil
	}
	sender, err := senderIn(chat, q.MessageID, q.FromMe, q.Participant)
	if err != nil {
		return nil, err
	}
	if q.FromMe {
		sender = ownJID(cli, chat)
	}
	if utf8.RuneCountInString(q.Text) > maxEditLength {
		return nil, fmt.Errorf("%w: replyTo.text is too long (max 4096 characters)", ErrInvalidMessageRef)
	}
	ci := &waE2E.ContextInfo{
		StanzaID:      proto.String(q.MessageID),
		QuotedMessage: &waE2E.Message{Conversation: proto.String(q.Text)},
	}
	if !sender.IsEmpty() {
		ci.Participant = proto.String(sender.ToNonAD().String())
	}
	return ci, nil
}

// ownJID is this number in the addressing the chat uses (LID chats quote by LID).
func ownJID(cli *whatsmeow.Client, chat types.JID) types.JID {
	if cli == nil || cli.Store == nil || cli.Store.ID == nil {
		return types.EmptyJID
	}
	if chat.Server == types.HiddenUserServer && !cli.Store.LID.IsEmpty() {
		return cli.Store.LID
	}
	return *cli.Store.ID
}

// withContext attaches reply information to the part of the message that carries it.
func withContext(msg *waE2E.Message, ci *waE2E.ContextInfo) *waE2E.Message {
	if ci == nil {
		return msg
	}
	switch {
	case msg.GetConversation() != "" || msg.Conversation != nil:
		// Plain text has no context: replies must be "extended" text.
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: msg.Conversation, ContextInfo: ci}}
	case msg.ExtendedTextMessage != nil:
		msg.ExtendedTextMessage.ContextInfo = ci
	case msg.ImageMessage != nil:
		msg.ImageMessage.ContextInfo = ci
	case msg.VideoMessage != nil:
		msg.VideoMessage.ContextInfo = ci
	case msg.AudioMessage != nil:
		msg.AudioMessage.ContextInfo = ci
	case msg.DocumentMessage != nil:
		msg.DocumentMessage.ContextInfo = ci
	}
	return msg
}

// QuotedRef is the message a received message replies to.
type QuotedRef struct {
	ID          string `json:"id"`
	Participant string `json:"participant,omitempty"`
}

// quotedOf returns what a received message replies to, or nil.
func quotedOf(m *waE2E.Message) *QuotedRef {
	var ci *waE2E.ContextInfo
	switch {
	case m == nil:
		return nil
	case m.GetExtendedTextMessage() != nil:
		ci = m.GetExtendedTextMessage().GetContextInfo()
	case m.GetImageMessage() != nil:
		ci = m.GetImageMessage().GetContextInfo()
	case m.GetVideoMessage() != nil:
		ci = m.GetVideoMessage().GetContextInfo()
	case m.GetAudioMessage() != nil:
		ci = m.GetAudioMessage().GetContextInfo()
	case m.GetDocumentMessage() != nil:
		ci = m.GetDocumentMessage().GetContextInfo()
	case m.GetStickerMessage() != nil:
		ci = m.GetStickerMessage().GetContextInfo()
	case m.GetLocationMessage() != nil:
		ci = m.GetLocationMessage().GetContextInfo()
	case m.GetContactMessage() != nil:
		ci = m.GetContactMessage().GetContextInfo()
	}
	if ci.GetStanzaID() == "" {
		return nil
	}
	return &QuotedRef{ID: ci.GetStanzaID(), Participant: ci.GetParticipant()}
}

// EditText changes the text of a message this number sent. WhatsApp only accepts edits for a
// short time after sending (about 15 minutes); later edits are ignored by the phones.
func (m *Manager) EditText(ctx context.Context, id, chatTo, messageID, text string) (string, error) {
	chat, err := ParseRecipient(chatTo)
	if err != nil {
		return "", err
	}
	if !validMessageID(messageID) {
		return "", fmt.Errorf("%w: messageId is missing or invalid", ErrInvalidMessageRef)
	}
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxEditLength {
		return "", fmt.Errorf("%w: text is required (max 4096 characters)", ErrInvalidMessageRef)
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return "", err
	}
	return m.paced(ctx, rt, cli, chat, cli.BuildEdit(chat, messageID, &waE2E.Message{Conversation: proto.String(text)}))
}

// DeleteMessage deletes a message for everyone. This number's own messages can always be deleted
// (WhatsApp limits how old they can be); in a group, an admin can also delete other people's.
func (m *Manager) DeleteMessage(ctx context.Context, id string, r MessageRef) (string, error) {
	chat, sender, err := resolveRef(r)
	if err != nil {
		return "", err
	}
	if !r.FromMe && chat.Server != types.GroupServer {
		return "", fmt.Errorf("%w: in a private chat only this number's own messages can be deleted for everyone", ErrInvalidMessageRef)
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return "", err
	}
	return m.paced(ctx, rt, cli, chat, cli.BuildRevoke(chat, sender, r.MessageID))
}
