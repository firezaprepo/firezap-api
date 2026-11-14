package instance

import (
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestPhoneVariants(t *testing.T) {
	for in, want := range map[string][]string{
		"5585991540577": {"5585991540577", "558591540577"}, // mobile with the ninth digit
		"558591540577":  {"558591540577", "5585991540577"}, // mobile without it
		"5511987654321": {"5511987654321", "551187654321"}, // São Paulo, same rule both ways
		"558532241234":  {"558532241234"},                  // landline (starts with 3): no variant
		"5585891540577": {"5585891540577"},                 // 13 digits but no 9 after the area code
		"14155550123":   {"14155550123"},                   // other countries untouched
		"351912345678":  {"351912345678"},                  // Portugal, starts with 3 not 55
	} {
		if got := phoneVariants(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", in, got, want)
		}
	}
}

func TestDirectoryTTL(t *testing.T) {
	var d directory
	now := time.Now()
	known := types.NewJID("558591540577", types.DefaultUserServer)
	d.put("5585991540577", known, now)
	d.put("5511900000000", types.EmptyJID, now) // not on WhatsApp
	if e, ok := d.get("5585991540577", now.Add(23*time.Hour)); !ok || e.jid != known {
		t.Fatalf("a known number is kept for a day: %v %v", e, ok)
	}
	if _, ok := d.get("5585991540577", now.Add(25*time.Hour)); ok {
		t.Fatal("a known number must be checked again after a day")
	}
	if _, ok := d.get("5511900000000", now.Add(30*time.Minute)); !ok {
		t.Fatal("an unknown number is remembered for a while")
	}
	if _, ok := d.get("5511900000000", now.Add(2*time.Hour)); ok {
		t.Fatal("an unknown number must be checked again after an hour (it may have joined)")
	}
	if _, ok := d.get("nope", now); ok {
		t.Fatal("never asked")
	}
}
