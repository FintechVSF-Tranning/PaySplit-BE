package domain

import (
	"encoding/json"
	"time"
)

// ActivityActor is the group member who performed a logged mutation, shown
// with their current public profile (not a historical snapshot).
type ActivityActor struct {
	MembershipID    string
	UserID          string
	DisplayName     string
	AvatarObjectKey *string
}

// Activity is one row of a group's timeline (spec 0002 AC-8).
type Activity struct {
	ID          string
	ActionType  string
	Description string
	Actor       ActivityActor
	// Metadata is passed through verbatim: it was written once, in the
	// mutation transaction, and is never recomputed on read.
	Metadata  json.RawMessage
	CreatedAt time.Time
}
