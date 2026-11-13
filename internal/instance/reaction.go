package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/types"
)

// ErrInvalidReaction is returned for a reaction request that cannot be sent.
var ErrInvalidReaction = errors.New("invalid reaction")

// maxReactionRunes allows the longest emoji sequences (skin tones and ZWJ families such as a
// couple with two skin tones use up to 10 code points) while refusing free text.
const maxReactionRunes = 16

// Reaction is a request to react to a message, or to remove a reaction (empty Emoji).
type Reaction struct {
	Chat        string // the chat of the message: "chat" from message.received
	MessageID   string // "id" from message.received
	FromMe      bool   // true when the message was sent by this number
	Participant string // groups only, when FromMe is false: "sender" from message.received
	Emoji       string // one emoji; empty removes this number's reaction
}

// validMessageID accepts WhatsApp message ids (letters and digits, at most 128 characters).
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

// validEmoji refuses text: a reaction is one emoji, so letters, digits and spaces are not
// allowed (the keycap emoji 1️⃣ starts with a digit, so digits followed by U+20E3 pass).
func validEmoji(s string) bool {
	if s == "" {
		return true // removes the reaction
	}
	if utf8.RuneCountInString(s) > maxReactionRunes {
		return false
	}
	if strings.ContainsRune(s, '⃣') { // keycap sequences: 0️⃣–9️⃣, #️⃣, *️⃣
		return true
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// reactionTarget resolves the chat and the original sender of the message being reacted to.
// whatsmeow marks the message as "from me" when the sender is empty or this number.
func reactionTarget(r Reaction) (chat, sender types.JID, err error) {
	if !validMessageID(r.MessageID) {
		return chat, sender, fmt.Errorf("%w: messageId is missing or invalid", ErrInvalidReaction)
	}
	if !validEmoji(r.Emoji) {
		return chat, sender, fmt.Errorf("%w: emoji must be a single emoji (empty removes the reaction)", ErrInvalidReaction)
	}
	if chat, err = ParseRecipient(r.Chat); err != nil {
		return chat, sender, err
	}
	switch {
	case r.FromMe:
		return chat, types.EmptyJID, nil
	case chat.Server == types.GroupServer:
		if strings.TrimSpace(r.Participant) == "" {
			return chat, sender, fmt.Errorf("%w: participant is required to react to someone else's message in a group", ErrInvalidReaction)
		}
		if sender, err = ParseRecipient(r.Participant); err != nil {
			return chat, sender, err
		}
		return chat, sender, nil
	default:
		// In a private chat, a message not sent by this number was sent by the other person.
		return chat, chat, nil
	}
}

// SendReaction reacts to a message (or removes the reaction) and returns the id of the
// reaction message. Reactions go through the same queue and limits as any other message.
func (m *Manager) SendReaction(ctx context.Context, id string, r Reaction) (string, error) {
	chat, sender, err := reactionTarget(r)
	if err != nil {
		return "", err
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return "", err
	}
	return m.paced(ctx, rt, cli, chat, cli.BuildReaction(chat, sender, r.MessageID, r.Emoji))
}
