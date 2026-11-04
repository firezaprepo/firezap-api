package instance

import (
	"errors"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// ErrInvalidRecipient is returned when "to" is not a phone number or a WhatsApp JID.
var ErrInvalidRecipient = errors.New("invalid recipient: use the phone number with country code (e.g. 5511999999999) or a JID")

// ParseRecipient turns user input into a JID. It accepts a phone number with country code
// (spaces, "+", "-", "(" and ")" are ignored) or a full JID such as "1203630@g.us".
func ParseRecipient(to string) (types.JID, error) {
	to = strings.TrimSpace(to)
	if strings.Contains(to, "@") {
		jid, err := types.ParseJID(to)
		if err != nil || jid.User == "" {
			return types.JID{}, ErrInvalidRecipient
		}
		return jid, nil
	}
	digits := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')':
			return -1
		default:
			return 'x' // any other character makes the number invalid
		}
	}, to)
	if strings.Contains(digits, "x") || len(digits) < 8 || len(digits) > 15 {
		return types.JID{}, ErrInvalidRecipient
	}
	return types.NewJID(digits, types.DefaultUserServer), nil
}

// MessageContent returns a simple kind ("text", "image", "video", "audio", "document",
// "sticker", "reaction", "location", "contact" or "other") and the text or caption of a message.
func MessageContent(m *waE2E.Message) (kind, text string) {
	switch {
	case m == nil:
		return "other", ""
	case m.GetConversation() != "":
		return "text", m.GetConversation()
	case m.GetExtendedTextMessage() != nil:
		return "text", m.GetExtendedTextMessage().GetText()
	case m.GetImageMessage() != nil:
		return "image", m.GetImageMessage().GetCaption()
	case m.GetVideoMessage() != nil:
		return "video", m.GetVideoMessage().GetCaption()
	case m.GetAudioMessage() != nil:
		return "audio", ""
	case m.GetDocumentMessage() != nil:
		return "document", m.GetDocumentMessage().GetCaption()
	case m.GetStickerMessage() != nil:
		return "sticker", ""
	case m.GetReactionMessage() != nil:
		return "reaction", m.GetReactionMessage().GetText()
	case m.GetLocationMessage() != nil:
		return "location", m.GetLocationMessage().GetName()
	case m.GetContactMessage() != nil:
		return "contact", m.GetContactMessage().GetDisplayName()
	default:
		return "other", ""
	}
}
