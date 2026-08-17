package domain

import "errors"

var (
	// ErrInvalidInput marks a request that failed validation.
	ErrInvalidInput = errors.New("invalid input")
	// ErrGroupNotFound is returned both when the group does not exist and when
	// the caller is not an active member, so group detail never reveals which.
	ErrGroupNotFound = errors.New("group not found")
	// ErrInvalidCursor marks a pagination cursor that failed to decode.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrCaptainRequired is returned when the caller is not the group's
	// active Captain. A nonexistent group also surfaces this error, so
	// invite mutations never reveal whether the group exists.
	ErrCaptainRequired = errors.New("active captain required")
	// ErrInviteNotFound is returned when the invite does not exist in the
	// target group.
	ErrInviteNotFound = errors.New("invite not found")
	// ErrInviteUnavailable covers expired, revoked, and exhausted invites
	// with a single public code so none is distinguishable from another.
	ErrInviteUnavailable = errors.New("invite unavailable")
	// ErrGroupMemberLimitReached is returned when a new join or
	// reactivation would push a group past 50 active members.
	ErrGroupMemberLimitReached = errors.New("group member limit reached")
	// ErrForbidden is returned when the caller may act on neither their
	// own membership nor, as Captain, another member's.
	ErrForbidden = errors.New("forbidden")
	// ErrCaptainTransferRequired is returned when leaving or removal
	// targets the active Captain: the role must be transferred first.
	ErrCaptainTransferRequired = errors.New("captain transfer required first")
	// ErrMemberNotFound is returned when a Captain transfer targets a
	// membership that does not exist or is not an active member.
	ErrMemberNotFound = errors.New("member not found")
	// ErrCaptainTransferConflict is returned when the group row lock for a
	// Captain transfer cannot be acquired immediately.
	ErrCaptainTransferConflict = errors.New("captain transfer conflict")
)

// OpenDebtsError carries the payable and receivable totals that block a
// member's exit, so the handler can expose them as error.fields (spec 0002
// AC-6).
type OpenDebtsError struct {
	PayableAmount    int64
	ReceivableAmount int64
}

func (e *OpenDebtsError) Error() string { return "member has open debts" }
