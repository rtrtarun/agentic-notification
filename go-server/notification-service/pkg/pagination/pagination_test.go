package pagination

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 123456789, time.UTC)

func TestACursorRoundTripsWithNanosecondPrecision(t *testing.T) {
	got, err := Decode(Encode(Cursor{At: t0, ID: "evt-42"}))
	if err != nil || !got.At.Equal(t0) || got.ID != "evt-42" {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestDecodeRefusesAnythingThatIsNotOurCursor(t *testing.T) {
	for name, s := range map[string]string{
		"empty":      "",
		"not base64": "!!!",
		"not json":   "bm90IGpzb24",
		"no id":      "eyJ0IjoxfQ",
		"too long":   strings.Repeat("a", 500),
		"json array": "WzFd",
	} {
		if _, err := Decode(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestOrderingIsNewestFirstWithTheIDBreakingTies(t *testing.T) {
	c := Cursor{At: t0, ID: "m"}
	cases := []struct {
		name string
		at   time.Time
		id   string
		want bool
	}{
		{"older", t0.Add(-time.Second), "z", true},
		{"newer", t0.Add(time.Second), "a", false},
		{"same time, smaller id comes after", t0, "a", true},
		{"same time, larger id comes before", t0, "z", false},
		{"the cursor item itself is not after itself", t0, "m", false},
	}
	for _, tc := range cases {
		if got := c.Before(tc.at, tc.id); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	if !Newer(t0.Add(time.Second), "a", t0, "z") || Newer(t0, "a", t0, "b") || !Newer(t0, "b", t0, "a") {
		t.Fatal("Newer ordering is wrong")
	}
}
