// Package pagination holds the cursor used to page through newest-first lists.
//
// A cursor names the last item a client received by its creation time and ID. The next
// page is everything strictly older, so pages do not shift when new items arrive, which
// numbered pages cannot promise. Clients treat a cursor as opaque.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// ErrInvalid is returned for a cursor that is malformed or was not made by this service.
var ErrInvalid = errors.New("invalid cursor")

const maxEncoded = 400

// Cursor is a position in a list ordered by creation time then ID, newest first.
type Cursor struct {
	At time.Time
	ID string
}

type wire struct {
	T int64  `json:"t"`
	I string `json:"i"`
}

// Encode returns the opaque string form of a cursor.
func Encode(c Cursor) string {
	raw, _ := json.Marshal(wire{T: c.At.UnixNano(), I: c.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode reads a cursor made by Encode.
func Decode(s string) (Cursor, error) {
	if s == "" || len(s) > maxEncoded {
		return Cursor{}, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalid
	}
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil || w.I == "" {
		return Cursor{}, ErrInvalid
	}
	return Cursor{At: time.Unix(0, w.T).UTC(), ID: w.I}, nil
}

// Before reports whether an item comes after the cursor in newest-first order, that is
// whether it is strictly older, with the ID breaking ties between equal times.
func (c Cursor) Before(at time.Time, id string) bool {
	if !at.Equal(c.At) {
		return at.Before(c.At)
	}
	return id < c.ID
}

// Newer reports whether a is listed before b in newest-first order.
func Newer(aAt time.Time, aID string, bAt time.Time, bID string) bool {
	if !aAt.Equal(bAt) {
		return aAt.After(bAt)
	}
	return aID > bID
}
