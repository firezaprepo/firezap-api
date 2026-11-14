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

// ErrInvalidReaction is returned for a reaction that is not a single emoji.
var ErrInvalidReaction = errors.New("invalid reaction")

// maxReactionRunes allows the longest emoji sequences (skin tones and ZWJ families such as a
// couple with two skin tones use up to 10 code points) while refusing free text.
const maxReactionRunes = 16

// Reaction is a request to react to a message, or to remove a reaction (empty Emoji).
type Reaction struct {
	MessageRef
	Emoji string // one emoji; empty removes this number's reaction
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
func reactionTarget(r Reaction) (chat, sender types.JID, err error) {
	if !validEmoji(r.Emoji) {
		return chat, sender, fmt.Errorf("%w: emoji must be a single emoji (empty removes the reaction)", ErrInvalidReaction)
	}
	return resolveRef(r.MessageRef)
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
	if chat, err = m.recipient(ctx, rt, cli, chat); err != nil {
		return "", err
	}
	if !r.FromMe && sender.Server == types.DefaultUserServer && chat.Server != types.GroupServer {
		sender = chat // the other person of a private chat, in the form WhatsApp has
	}
	return m.paced(ctx, rt, cli, chat, cli.BuildReaction(chat, sender, r.MessageID, r.Emoji))
}
