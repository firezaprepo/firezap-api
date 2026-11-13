package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// ErrInvalidGroup is returned for a group request that cannot work.
var ErrInvalidGroup = errors.New("invalid group request")

const (
	maxGroupName        = 100  // WhatsApp's limit for a group subject
	maxGroupDescription = 2048 // WhatsApp's limit for a group description
	// maxParticipantsPerRequest bounds one add/remove/promote/demote call. Adding people who
	// did not ask to be in a group is the classic spam pattern, so every person added also
	// counts as one message in the sending limits; invite links are the better way to grow.
	maxParticipantsPerRequest = 50
)

// Group is the public view of a WhatsApp group.
type Group struct {
	JID          string        `json:"jid"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Owner        string        `json:"owner,omitempty"`
	OwnerPn      string        `json:"ownerPn,omitempty"`
	Announce     bool          `json:"announce"` // only admins can send messages
	Locked       bool          `json:"locked"`   // only admins can edit the group info
	CreatedAt    time.Time     `json:"createdAt,omitempty"`
	Size         int           `json:"size"`
	Participants []Participant `json:"participants,omitempty"`
}

// Participant is a member of a group, or the result of changing one.
type Participant struct {
	JID          string `json:"jid"`
	Phone        string `json:"phone,omitempty"` // phone-number JID, when WhatsApp tells it
	IsAdmin      bool   `json:"isAdmin"`
	IsSuperAdmin bool   `json:"isSuperAdmin"`
	// Error is WhatsApp's code when a change failed for this person (for example 403: their
	// privacy settings do not allow being added; 409: already in the group).
	Error int `json:"error,omitempty"`
}

// GroupUpdate changes the group info. Nil fields are left as they are.
type GroupUpdate struct {
	Name        *string
	Description *string
	Announce    *bool
	Locked      *bool
}

// ParseGroupJID accepts "120363…@g.us" or the bare id "120363…".
func ParseGroupJID(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if s != "" && !strings.Contains(s, "@") {
		s += "@" + types.GroupServer
	}
	jid, err := types.ParseJID(s)
	if err != nil || jid.Server != types.GroupServer || jid.User == "" {
		return types.JID{}, fmt.Errorf("%w: not a group id (expected 120363…@g.us)", ErrInvalidGroup)
	}
	return jid, nil
}

// parseParticipants turns phone numbers or user JIDs into JIDs, without duplicates.
func parseParticipants(list []string) ([]types.JID, error) {
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: participants is empty", ErrInvalidGroup)
	}
	if len(list) > maxParticipantsPerRequest {
		return nil, fmt.Errorf("%w: at most %d participants per request", ErrInvalidGroup, maxParticipantsPerRequest)
	}
	seen := map[types.JID]bool{}
	out := make([]types.JID, 0, len(list))
	for _, p := range list {
		jid, err := ParseRecipient(p)
		if err != nil || (jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer) {
			return nil, fmt.Errorf("%w: participant %q is not a phone number or user id", ErrInvalidGroup, p)
		}
		jid = jid.ToNonAD()
		if !seen[jid] {
			seen[jid] = true
			out = append(out, jid)
		}
	}
	return out, nil
}

func validGroupName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > maxGroupName {
		return fmt.Errorf("%w: name is required (max %d characters)", ErrInvalidGroup, maxGroupName)
	}
	return nil
}

func jidString(j types.JID) string {
	if j.IsEmpty() {
		return ""
	}
	return j.ToNonAD().String()
}

func toGroup(g *types.GroupInfo, withParticipants bool) Group {
	out := Group{
		JID: g.JID.String(), Name: g.Name, Description: g.Topic,
		Owner: jidString(g.OwnerJID), OwnerPn: jidString(g.OwnerPN),
		Announce: g.IsAnnounce, Locked: g.IsLocked, CreatedAt: g.GroupCreated.UTC(),
		Size: len(g.Participants),
	}
	if g.ParticipantCount > out.Size {
		out.Size = g.ParticipantCount
	}
	if withParticipants {
		out.Participants = toParticipants(g.Participants)
	}
	return out
}

func toParticipants(ps []types.GroupParticipant) []Participant {
	out := make([]Participant, 0, len(ps))
	for _, p := range ps {
		out = append(out, Participant{JID: jidString(p.JID), Phone: jidString(p.PhoneNumber),
			IsAdmin: p.IsAdmin || p.IsSuperAdmin, IsSuperAdmin: p.IsSuperAdmin, Error: p.Error})
	}
	return out
}

// inviteCode extracts the code of an invite link ("https://chat.whatsapp.com/<code>") or
// accepts the bare code.
func inviteCode(link string) (string, error) {
	code := strings.TrimSpace(link)
	for _, p := range []string{"https://chat.whatsapp.com/invite/", "https://chat.whatsapp.com/", "chat.whatsapp.com/"} {
		code = strings.TrimPrefix(code, p)
	}
	code, _, _ = strings.Cut(code, "?")
	if len(code) < 10 || len(code) > 40 {
		return "", fmt.Errorf("%w: invite link must look like https://chat.whatsapp.com/<code>", ErrInvalidGroup)
	}
	for _, r := range code {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return "", fmt.Errorf("%w: invite link must look like https://chat.whatsapp.com/<code>", ErrInvalidGroup)
		}
	}
	return code, nil
}

// Groups lists the groups this number is in (without the member lists).
func (m *Manager) Groups(ctx context.Context, id string) ([]Group, error) {
	_, cli, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	gs, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		out = append(out, toGroup(g, false))
	}
	return out, nil
}

// GroupInfo returns one group with its members.
func (m *Manager) GroupInfo(ctx context.Context, id, group string) (Group, error) {
	jid, err := ParseGroupJID(group)
	if err != nil {
		return Group{}, err
	}
	_, cli, err := m.connected(id)
	if err != nil {
		return Group{}, err
	}
	g, err := cli.GetGroupInfo(ctx, jid)
	if err != nil {
		return Group{}, err
	}
	return toGroup(g, true), nil
}

// CreateGroup creates a group with this number as admin, with or without participants. Each
// person added counts as one message in the sending limits.
func (m *Manager) CreateGroup(ctx context.Context, id, name string, participants []string) (Group, error) {
	if err := validGroupName(name); err != nil {
		return Group{}, err
	}
	var people []types.JID // optional: create the group alone and share the invite link
	if len(participants) > 0 {
		var err error
		if people, err = parseParticipants(participants); err != nil {
			return Group{}, err
		}
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return Group{}, err
	}
	var g *types.GroupInfo
	err = m.throttled(ctx, rt, 1+len(people), func() error {
		g, err = cli.CreateGroup(ctx, whatsmeow.ReqCreateGroup{Name: strings.TrimSpace(name), Participants: people})
		return err
	})
	if err != nil {
		return Group{}, err
	}
	return toGroup(g, true), nil
}

// UpdateGroup changes the name, description or settings. This number must be an admin when
// the group is locked.
func (m *Manager) UpdateGroup(ctx context.Context, id, group string, u GroupUpdate) error {
	jid, err := ParseGroupJID(group)
	if err != nil {
		return err
	}
	if u.Name == nil && u.Description == nil && u.Announce == nil && u.Locked == nil {
		return fmt.Errorf("%w: nothing to change (name, description, announce or locked)", ErrInvalidGroup)
	}
	if u.Name != nil {
		if err := validGroupName(*u.Name); err != nil {
			return err
		}
	}
	if u.Description != nil && utf8.RuneCountInString(*u.Description) > maxGroupDescription {
		return fmt.Errorf("%w: description is too long (max %d characters)", ErrInvalidGroup, maxGroupDescription)
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return err
	}
	return m.throttled(ctx, rt, 1, func() error {
		if u.Name != nil {
			if err := cli.SetGroupName(ctx, jid, strings.TrimSpace(*u.Name)); err != nil {
				return err
			}
		}
		if u.Description != nil {
			if err := cli.SetGroupTopic(ctx, jid, "", "", *u.Description); err != nil {
				return err
			}
		}
		if u.Announce != nil {
			if err := cli.SetGroupAnnounce(ctx, jid, *u.Announce); err != nil {
				return err
			}
		}
		if u.Locked != nil {
			return cli.SetGroupLocked(ctx, jid, *u.Locked)
		}
		return nil
	})
}

// UpdateParticipants adds, removes, promotes or demotes members. This number must be an
// admin. Each person added counts as one message in the sending limits.
func (m *Manager) UpdateParticipants(ctx context.Context, id, group, action string, participants []string) ([]Participant, error) {
	jid, err := ParseGroupJID(group)
	if err != nil {
		return nil, err
	}
	change := whatsmeow.ParticipantChange(strings.ToLower(strings.TrimSpace(action)))
	switch change {
	case whatsmeow.ParticipantChangeAdd, whatsmeow.ParticipantChangeRemove,
		whatsmeow.ParticipantChangePromote, whatsmeow.ParticipantChangeDemote:
	default:
		return nil, fmt.Errorf("%w: action must be add, remove, promote or demote", ErrInvalidGroup)
	}
	people, err := parseParticipants(participants)
	if err != nil {
		return nil, err
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	units := 1
	if change == whatsmeow.ParticipantChangeAdd {
		units = len(people)
	}
	var res []types.GroupParticipant
	err = m.throttled(ctx, rt, units, func() error {
		res, err = cli.UpdateGroupParticipants(ctx, jid, people, change)
		return err
	})
	if err != nil {
		return nil, err
	}
	return toParticipants(res), nil
}

// InviteLink returns the group's invite link; reset revokes the current link and makes a new one.
func (m *Manager) InviteLink(ctx context.Context, id, group string, reset bool) (string, error) {
	jid, err := ParseGroupJID(group)
	if err != nil {
		return "", err
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return "", err
	}
	if !reset {
		return cli.GetGroupInviteLink(ctx, jid, false)
	}
	var link string
	err = m.throttled(ctx, rt, 1, func() error {
		link, err = cli.GetGroupInviteLink(ctx, jid, true)
		return err
	})
	return link, err
}

// JoinGroup joins a group with an invite link and returns the group id.
func (m *Manager) JoinGroup(ctx context.Context, id, link string) (string, error) {
	code, err := inviteCode(link)
	if err != nil {
		return "", err
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return "", err
	}
	var jid types.JID
	err = m.throttled(ctx, rt, 1, func() error {
		jid, err = cli.JoinGroupWithLink(ctx, code)
		return err
	})
	if err != nil {
		return "", err
	}
	return jid.String(), nil
}

// LeaveGroup makes this number leave a group.
func (m *Manager) LeaveGroup(ctx context.Context, id, group string) error {
	jid, err := ParseGroupJID(group)
	if err != nil {
		return err
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return err
	}
	return m.throttled(ctx, rt, 1, func() error { return cli.LeaveGroup(ctx, jid) })
}

// groupChange is what changed in a group, ready for the webhook.
type groupChange struct {
	Chat, Sender, SenderPn       string
	Name, Description            *string
	Announce, Locked             *bool
	Join, Leave, Promote, Demote []string
	JoinReason                   string
	Joined                       bool // this number joined or was added to the group
	InviteReset                  bool // the invite link was revoked and a new one made (the link itself is not sent)
}

func (c groupChange) data() map[string]any {
	d := map[string]any{"chat": c.Chat}
	set := func(k string, v any, ok bool) {
		if ok {
			d[k] = v
		}
	}
	set("sender", c.Sender, c.Sender != "")
	set("senderPn", c.SenderPn, c.SenderPn != "")
	set("name", deref(c.Name), c.Name != nil)
	set("description", deref(c.Description), c.Description != nil)
	set("announce", derefBool(c.Announce), c.Announce != nil)
	set("locked", derefBool(c.Locked), c.Locked != nil)
	set("join", c.Join, len(c.Join) > 0)
	set("leave", c.Leave, len(c.Leave) > 0)
	set("promote", c.Promote, len(c.Promote) > 0)
	set("demote", c.Demote, len(c.Demote) > 0)
	set("joinReason", c.JoinReason, c.JoinReason != "")
	set("joined", true, c.Joined)
	set("inviteReset", true, c.InviteReset)
	return d
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefBool(b *bool) bool { return b != nil && *b }

func jidStrings(js []types.JID) []string {
	out := make([]string, 0, len(js))
	for _, j := range js {
		out = append(out, jidString(j))
	}
	return out
}
