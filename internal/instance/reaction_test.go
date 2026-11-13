package instance

import (
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestValidEmoji(t *testing.T) {
	for _, ok := range []string{"", "👍", "❤️", "👍🏽", "👨‍👩‍👧‍👦", "🇧🇷", "1️⃣", "#️⃣"} {
		if !validEmoji(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"ok", "👍 ok", "1", " ", "\n", "👍👍👍👍👍👍👍👍👍👍👍👍👍👍👍👍👍"} {
		if validEmoji(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestReactionTarget(t *testing.T) {
	const id = "3EB0F1E313827D8CDEB5AE"
	// Private chat, message from the other person: the sender is the chat.
	chat, sender, err := reactionTarget(Reaction{MessageRef: MessageRef{Chat: "5511987654321", MessageID: id}, Emoji: "👍"})
	if err != nil || sender != chat || chat.Server != types.DefaultUserServer {
		t.Fatalf("private: chat=%v sender=%v err=%v", chat, sender, err)
	}
	// Own message: empty sender, so whatsmeow marks it as from me.
	if _, sender, err = reactionTarget(Reaction{MessageRef: MessageRef{Chat: "5511987654321", MessageID: id, FromMe: true}, Emoji: "👍"}); err != nil || !sender.IsEmpty() {
		t.Fatalf("fromMe: sender=%v err=%v", sender, err)
	}
	// Group, someone else's message: participant required.
	_, _, err = reactionTarget(Reaction{MessageRef: MessageRef{Chat: "120363000000000000@g.us", MessageID: id}, Emoji: "👍"})
	if !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("group without participant should fail, got %v", err)
	}
	chat, sender, err = reactionTarget(Reaction{MessageRef: MessageRef{Chat: "120363000000000000@g.us", MessageID: id, Participant: "123456789012345@lid"}, Emoji: "👍"})
	if err != nil || chat.Server != types.GroupServer || sender.Server != types.HiddenUserServer {
		t.Fatalf("group: chat=%v sender=%v err=%v", chat, sender, err)
	}
	// Bad message id and text instead of an emoji.
	if _, _, err = reactionTarget(Reaction{MessageRef: MessageRef{Chat: "5511987654321", MessageID: "a b"}, Emoji: "👍"}); !errors.Is(err, ErrInvalidMessageRef) {
		t.Fatalf("bad id should fail, got %v", err)
	}
	if _, _, err = reactionTarget(Reaction{MessageRef: MessageRef{Chat: "5511987654321", MessageID: id}, Emoji: "great"}); !errors.Is(err, ErrInvalidReaction) {
		t.Fatalf("text emoji should fail, got %v", err)
	}
}

func TestDescribeReaction(t *testing.T) {
	m := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key:  &waCommon.MessageKey{ID: proto.String("ABC123")},
		Text: proto.String("❤️"),
	}}
	if d := Describe(m); !d.Deliver || d.Kind != "reaction" || d.Text != "❤️" || d.Target != "ABC123" {
		t.Fatalf("reaction: %+v", d)
	}
	removed := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("ABC123")}, Text: proto.String("")}}
	if d := Describe(removed); !d.Deliver || d.Kind != "reaction" || d.Text != "" || d.Target != "ABC123" {
		t.Fatalf("removed reaction: %+v", d)
	}
}
