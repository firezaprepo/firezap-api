package instance

import (
	"context"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Description is what a received message means for an integration.
type Description struct {
	Kind    string // see MessageContent, plus "revoke" and "edit"
	Text    string
	Target  string // for "revoke" and "edit": the id of the message it changes
	Deliver bool   // false for protocol-only messages, which carry nothing a person sees
	Media   *Media // the attachment of image, video, audio, document and sticker messages
}

// Describe classifies a received message.
//
// Group messages arrive in two parts under the SAME message id: a sender-key distribution
// part (encryption plumbing) and the content itself. Delivering both made integrations show
// the message twice (end-to-end test of 2026-11-10). Protocol messages that only manage keys,
// history sync or chat settings are dropped the same way. Deletions and edits are protocol
// messages too, but they matter to integrations, so they become "revoke" and "edit".
func Describe(m *waE2E.Message) Description {
	if m == nil {
		return Description{Kind: "other"}
	}
	if pm := m.GetProtocolMessage(); pm != nil {
		switch pm.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			return Description{Kind: "revoke", Target: pm.GetKey().GetID(), Deliver: true}
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			_, text := MessageContent(pm.GetEditedMessage())
			return Description{Kind: "edit", Text: text, Target: pm.GetKey().GetID(), Deliver: true}
		default:
			return Description{Kind: "protocol"}
		}
	}
	kind, text := MessageContent(m)
	if kind == "other" && isPlumbingOnly(m) {
		return Description{Kind: "other"}
	}
	return Description{Kind: kind, Text: text, Deliver: true, Media: MediaOf(m)}
}

// isPlumbingOnly reports whether a message has nothing but encryption or context metadata:
// once the sender-key distribution part and the message context info are removed, nothing
// is left.
func isPlumbingOnly(m *waE2E.Message) bool {
	c, ok := proto.Clone(m).(*waE2E.Message)
	if !ok {
		return false
	}
	c.SenderKeyDistributionMessage = nil
	c.MessageContextInfo = nil
	return proto.Size(c) == 0
}

// phoneJID returns the phone-number JID ("<digits>@s.whatsapp.net") of a user, or "" when it
// is unknown. WhatsApp now addresses many chats by LID ("…@lid"), an id that is not a phone
// number; integrations that key conversations by phone need the number (2026-11-10 test: the
// sender of a private message arrived only as a LID). Order: the JID itself if it is already a
// phone number, then the alternate address WhatsApp sent with the message, then the LID ↔
// phone map that whatsmeow keeps.
func phoneJID(ctx context.Context, lids lidLookup, jid, alt types.JID) string {
	switch {
	case jid.Server == types.DefaultUserServer:
		return jid.ToNonAD().String()
	case alt.Server == types.DefaultUserServer:
		return alt.ToNonAD().String()
	case jid.Server == types.HiddenUserServer && lids != nil:
		if pn, err := lids.GetPNForLID(ctx, jid.ToNonAD()); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD().String()
		}
	}
	return ""
}

// lidLookup is the part of whatsmeow's LID store used here (an interface, so tests can fake it).
type lidLookup interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
}

// ChatType tells integrations what kind of chat a message belongs to: "private", "group",
// "status" (WhatsApp Status, the 24-hour stories), "broadcast" (a broadcast list) or
// "newsletter" (a Channel). Statuses and channel posts arrive as messages too; without this an
// integration could open a support conversation for someone's story (2026-11-10 test: a Status
// arrived flagged as a group and a Channel post as a private chat without a phone number).
func ChatType(chat types.JID) string {
	switch chat.Server {
	case types.GroupServer:
		return "group"
	case types.NewsletterServer:
		return "newsletter"
	case types.BroadcastServer:
		if chat.User == types.StatusBroadcastJID.User {
			return "status"
		}
		return "broadcast"
	default:
		return "private"
	}
}
