package realtime

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const (
	ChannelGroupEvents = "group_events"
	ChannelBillEvents  = "bill_events"
	ChannelUserEvents  = "user_events"

	MaxNotifyPayload = 7000
	SchemaVersion    = 1

	KindInvalidate    = "invalidate"
	KindStreamReplace = "stream.replace"
	KindSessionEnded  = "session.ended"

	ScopeHome       = "home"
	ScopeGroup      = "group"
	ScopeBill       = "bill"
	ScopeSettlement = "settlement"
)

var (
	ErrInvalidJSON          = errors.New("invalid_json")
	ErrUnknownSchema        = errors.New("unknown_schema")
	ErrUnknownKind          = errors.New("unknown_kind")
	ErrMissingRecipient     = errors.New("missing_recipient")
	ErrConflictingRecipient = errors.New("conflicting_recipient")
	ErrInvalidUUID          = errors.New("invalid_uuid")
	ErrInvalidBody          = errors.New("invalid_body")
	ErrOversized            = errors.New("oversized")
	ErrMissingGroupID       = errors.New("missing_group_id")
	ErrMissingBillID        = errors.New("missing_bill_id")
	ErrInvalidVersion       = errors.New("invalid_version")
	ErrMissingType          = errors.New("missing_type")
)

type GroupEnvelope struct {
	GroupID         uuid.UUID       `json:"group_id"`
	Version         int64           `json:"version"`
	Type            string          `json:"type"`
	Data            json.RawMessage `json:"data,omitempty"`
	AudienceUserIDs []uuid.UUID     `json:"audience_user_ids"`
}

type BillEnvelope struct {
	GroupID         uuid.UUID       `json:"group_id"`
	BillID          uuid.UUID       `json:"bill_id"`
	Type            string          `json:"type"`
	Data            json.RawMessage `json:"data,omitempty"`
	AudienceUserIDs []uuid.UUID     `json:"audience_user_ids"`
}

type InvalidateBody struct {
	Scope           string     `json:"scope"`
	GroupID         uuid.UUID  `json:"group_id"`
	ResourceID      *uuid.UUID `json:"resource_id,omitempty"`
	ResourceVersion *int32     `json:"resource_version,omitempty"`
	Type            string     `json:"type"`
}

type UserEnvelope struct {
	SchemaVersion       int             `json:"schema_version"`
	Kind                string          `json:"kind"`
	AudienceUserIDs     []uuid.UUID     `json:"audience_user_ids,omitempty"`
	Body                *InvalidateBody `json:"body,omitempty"`
	TargetSIDs          []uuid.UUID     `json:"target_sids,omitempty"`
	ReplacementStreamID *uuid.UUID      `json:"replacement_stream_id,omitempty"`
}

func DecodeGroupEnvelope(payload string) (GroupEnvelope, error) {
	if len(payload) > MaxNotifyPayload {
		return GroupEnvelope{}, ErrOversized
	}
	var env GroupEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return GroupEnvelope{}, ErrInvalidJSON
	}
	if env.GroupID == uuid.Nil {
		return GroupEnvelope{}, ErrMissingGroupID
	}
	if env.Version <= 0 {
		return GroupEnvelope{}, ErrInvalidVersion
	}
	if strings.TrimSpace(env.Type) == "" {
		return GroupEnvelope{}, ErrMissingType
	}
	env.AudienceUserIDs = NormalizeAudience(env.AudienceUserIDs)
	if len(env.AudienceUserIDs) == 0 {
		return GroupEnvelope{}, ErrMissingRecipient
	}
	return env, nil
}

func DecodeBillEnvelope(payload string) (BillEnvelope, error) {
	if len(payload) > MaxNotifyPayload {
		return BillEnvelope{}, ErrOversized
	}
	var env BillEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return BillEnvelope{}, ErrInvalidJSON
	}
	if env.GroupID == uuid.Nil {
		return BillEnvelope{}, ErrMissingGroupID
	}
	if env.BillID == uuid.Nil {
		return BillEnvelope{}, ErrMissingBillID
	}
	if strings.TrimSpace(env.Type) == "" {
		return BillEnvelope{}, ErrMissingType
	}
	env.AudienceUserIDs = NormalizeAudience(env.AudienceUserIDs)
	if len(env.AudienceUserIDs) == 0 {
		return BillEnvelope{}, ErrMissingRecipient
	}
	return env, nil
}

func DecodeUserEnvelope(payload string) (UserEnvelope, error) {
	if len(payload) > MaxNotifyPayload {
		return UserEnvelope{}, ErrOversized
	}
	var env UserEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return UserEnvelope{}, ErrInvalidJSON
	}
	if env.SchemaVersion != SchemaVersion {
		return UserEnvelope{}, ErrUnknownSchema
	}
	env.AudienceUserIDs = NormalizeAudience(env.AudienceUserIDs)
	// Control phiên không bị cắt: bỏ sót một SID nghĩa là stream của phiên đó
	// vẫn sống sau khi đã bị thu hồi.
	env.TargetSIDs = NormalizeSIDs(env.TargetSIDs)
	hasAudience := len(env.AudienceUserIDs) > 0
	hasTargets := len(env.TargetSIDs) > 0
	if hasAudience && hasTargets {
		return UserEnvelope{}, ErrConflictingRecipient
	}
	switch env.Kind {
	case KindInvalidate:
		if !hasAudience {
			return UserEnvelope{}, ErrMissingRecipient
		}
		if hasTargets || env.ReplacementStreamID != nil {
			return UserEnvelope{}, ErrInvalidBody
		}
		if env.Body == nil {
			return UserEnvelope{}, ErrInvalidBody
		}
		if err := validateInvalidateBody(*env.Body); err != nil {
			return UserEnvelope{}, err
		}
	case KindStreamReplace:
		if hasAudience || env.Body != nil {
			return UserEnvelope{}, ErrInvalidBody
		}
		if !hasTargets {
			return UserEnvelope{}, ErrMissingRecipient
		}
		if env.ReplacementStreamID == nil || *env.ReplacementStreamID == uuid.Nil {
			return UserEnvelope{}, ErrInvalidUUID
		}
	case KindSessionEnded:
		if hasAudience || env.Body != nil || env.ReplacementStreamID != nil {
			return UserEnvelope{}, ErrInvalidBody
		}
		if !hasTargets {
			return UserEnvelope{}, ErrMissingRecipient
		}
	default:
		return UserEnvelope{}, ErrUnknownKind
	}
	return env, nil
}

func validateInvalidateBody(body InvalidateBody) error {
	switch body.Scope {
	case ScopeHome, ScopeGroup, ScopeBill, ScopeSettlement:
	default:
		return ErrInvalidBody
	}
	if body.GroupID == uuid.Nil {
		return ErrInvalidUUID
	}
	if strings.TrimSpace(body.Type) == "" {
		return ErrInvalidBody
	}
	if body.ResourceID != nil && *body.ResourceID == uuid.Nil {
		return ErrInvalidUUID
	}
	return nil
}

func EncodeGroupEnvelope(env GroupEnvelope) ([]byte, bool, error) {
	env.AudienceUserIDs = NormalizeAudience(env.AudienceUserIDs)
	if env.GroupID == uuid.Nil || env.Version <= 0 || strings.TrimSpace(env.Type) == "" || len(env.AudienceUserIDs) == 0 {
		return nil, false, ErrInvalidBody
	}
	full := map[string]any{
		"group_id":          env.GroupID.String(),
		"version":           env.Version,
		"type":              env.Type,
		"audience_user_ids": AudienceStrings(env.AudienceUserIDs),
	}
	if len(env.Data) > 0 {
		full["data"] = json.RawMessage(env.Data)
	}
	payload, err := json.Marshal(full)
	if err != nil {
		return nil, false, err
	}
	if len(payload) <= MaxNotifyPayload {
		return payload, false, nil
	}
	delete(full, "data")
	thin, err := json.Marshal(full)
	if err != nil {
		return nil, false, err
	}
	if len(thin) > MaxNotifyPayload {
		return nil, false, ErrOversized
	}
	return thin, true, nil
}

func EncodeBillEnvelope(env BillEnvelope) ([]byte, error) {
	env.AudienceUserIDs = NormalizeAudience(env.AudienceUserIDs)
	if env.GroupID == uuid.Nil || env.BillID == uuid.Nil || strings.TrimSpace(env.Type) == "" || len(env.AudienceUserIDs) == 0 {
		return nil, ErrInvalidBody
	}
	full := map[string]any{
		"group_id":          env.GroupID.String(),
		"bill_id":           env.BillID.String(),
		"type":              env.Type,
		"audience_user_ids": AudienceStrings(env.AudienceUserIDs),
	}
	if len(env.Data) > 0 {
		full["data"] = json.RawMessage(env.Data)
	}
	payload, err := json.Marshal(full)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxNotifyPayload {
		return nil, ErrOversized
	}
	return payload, nil
}

func EncodeInvalidate(audience []uuid.UUID, body InvalidateBody) ([]byte, error) {
	audience = NormalizeAudience(audience)
	if len(audience) == 0 {
		return nil, ErrMissingRecipient
	}
	if err := validateInvalidateBody(body); err != nil {
		return nil, err
	}
	env := UserEnvelope{
		SchemaVersion:   SchemaVersion,
		Kind:            KindInvalidate,
		AudienceUserIDs: audience,
		Body:            &body,
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxNotifyPayload {
		return nil, ErrOversized
	}
	return payload, nil
}

func EncodeStreamReplace(targetSID, replacementStreamID uuid.UUID) ([]byte, error) {
	if targetSID == uuid.Nil || replacementStreamID == uuid.Nil {
		return nil, ErrInvalidUUID
	}
	payload, err := json.Marshal(UserEnvelope{
		SchemaVersion:       SchemaVersion,
		Kind:                KindStreamReplace,
		TargetSIDs:          []uuid.UUID{targetSID},
		ReplacementStreamID: &replacementStreamID,
	})
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxNotifyPayload {
		return nil, ErrOversized
	}
	return payload, nil
}

func EncodeSessionEnded(sids []uuid.UUID) ([]byte, error) {
	sids = NormalizeSIDs(sids)
	if len(sids) == 0 {
		return nil, ErrMissingRecipient
	}
	payload, err := json.Marshal(UserEnvelope{
		SchemaVersion: SchemaVersion,
		Kind:          KindSessionEnded,
		TargetSIDs:    sids,
	})
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxNotifyPayload {
		return nil, ErrOversized
	}
	return payload, nil
}
