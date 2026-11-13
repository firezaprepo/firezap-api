package instance

import (
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestParseGroupJID(t *testing.T) {
	for _, ok := range []string{"120363000000000000@g.us", "120363000000000000", " 120363000000000000@g.us "} {
		if j, err := ParseGroupJID(ok); err != nil || j.String() != "120363000000000000@g.us" {
			t.Errorf("%q: %v %v", ok, j, err)
		}
	}
	for _, bad := range []string{"", "5511987654321@s.whatsapp.net", "@g.us", "abc@lid"} {
		if _, err := ParseGroupJID(bad); !errors.Is(err, ErrInvalidGroup) {
			t.Errorf("%q should be refused, got %v", bad, err)
		}
	}
}

func TestParseParticipants(t *testing.T) {
	got, err := parseParticipants([]string{"+55 11 98765-4321", "5511987654321", "123456789@lid"})
	if err != nil || len(got) != 2 { // the same number twice counts once
		t.Fatalf("got %v %v", got, err)
	}
	if got[1].Server != types.HiddenUserServer {
		t.Fatalf("LID participant: %v", got[1])
	}
	if _, err := parseParticipants(nil); !errors.Is(err, ErrInvalidGroup) {
		t.Fatalf("empty list: %v", err)
	}
	if _, err := parseParticipants([]string{"120363000000000000@g.us"}); !errors.Is(err, ErrInvalidGroup) {
		t.Fatalf("a group is not a participant: %v", err)
	}
	many := make([]string, maxParticipantsPerRequest+1)
	for i := range many {
		many[i] = "55119876543" + string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	if _, err := parseParticipants(many); !errors.Is(err, ErrInvalidGroup) {
		t.Fatalf("over the per-request limit: %v", err)
	}
}

func TestInviteCode(t *testing.T) {
	for in, want := range map[string]string{
		"https://chat.whatsapp.com/AbCdEfGhIjKlMnOpQrStUv":        "AbCdEfGhIjKlMnOpQrStUv",
		"https://chat.whatsapp.com/invite/AbCdEfGhIjKlMnOpQrStUv": "AbCdEfGhIjKlMnOpQrStUv",
		"AbCdEfGhIjKlMnOpQrStUv":                                  "AbCdEfGhIjKlMnOpQrStUv",
		"https://chat.whatsapp.com/AbCdEfGhIjKlMnOpQrStUv?mode=r": "AbCdEfGhIjKlMnOpQrStUv",
	} {
		if got, err := inviteCode(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "short", "https://evil.example/AbCdEfGhIjKlMnOpQrStUv", "AbCd/../EfGhIjKlMnOp"} {
		if _, err := inviteCode(bad); !errors.Is(err, ErrInvalidGroup) {
			t.Errorf("%q should be refused, got %v", bad, err)
		}
	}
}

func TestValidGroupName(t *testing.T) {
	if validGroupName("Clientes VIP") != nil {
		t.Fatal("normal name refused")
	}
	if validGroupName("  ") == nil || validGroupName(strings.Repeat("a", maxGroupName+1)) == nil {
		t.Fatal("blank or too long name accepted")
	}
}

func TestGroupChangeData(t *testing.T) {
	name, locked := "Novo nome", true
	d := groupChange{Chat: "1@g.us", Sender: "2@lid", Name: &name, Locked: &locked, Join: []string{"3@lid"}}.data()
	if d["name"] != "Novo nome" || d["locked"] != true || len(d["join"].([]string)) != 1 || d["sender"] != "2@lid" {
		t.Fatalf("data: %v", d)
	}
	for _, absent := range []string{"description", "announce", "leave", "joined"} {
		if _, ok := d[absent]; ok {
			t.Errorf("%s should be absent when it did not change: %v", absent, d)
		}
	}
}

func TestToGroup(t *testing.T) {
	g := &types.GroupInfo{JID: types.NewJID("1", types.GroupServer), GroupName: types.GroupName{Name: "G"},
		Participants: []types.GroupParticipant{
			{JID: types.NewJID("2", types.HiddenUserServer), PhoneNumber: types.NewJID("5511987654321", types.DefaultUserServer), IsSuperAdmin: true},
			{JID: types.NewJID("3", types.HiddenUserServer), Error: 403},
		}}
	out := toGroup(g, true)
	if out.Size != 2 || !out.Participants[0].IsAdmin || out.Participants[0].Phone != "5511987654321@s.whatsapp.net" || out.Participants[1].Error != 403 {
		t.Fatalf("group: %+v", out)
	}
	if toGroup(g, false).Participants != nil {
		t.Fatal("list view should not carry the members")
	}
}
