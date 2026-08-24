package http

import (
	"bytes"
	"encoding/json"
	"errors"
)

type createGroupRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency,omitempty"`
}

type createInviteRequest struct {
	ExpiresInHours optionalInt  `json:"expires_in_hours"`
	MaxUses        optionalInt  `json:"max_uses"`
	Regenerate     optionalBool `json:"regenerate"`
}

// UnmarshalJSON keeps the optional-body contract narrow: an omitted body is
// accepted by ReadOptionalJSON, but a supplied value must be an object. The
// alias avoids recursively invoking this method while preserving strict
// unknown-field handling for this custom decoder.
func (r *createInviteRequest) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("invite request body must be a JSON object")
	}

	type requestAlias createInviteRequest
	var decoded requestAlias
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*r = createInviteRequest(decoded)
	return nil
}

func (r *createInviteRequest) hasConfiguration() bool {
	return r.ExpiresInHours.Set || r.MaxUses.Set || r.Regenerate.Set
}

func (r *createInviteRequest) hasNullConfiguration() bool {
	return r.ExpiresInHours.Null || r.MaxUses.Null || r.Regenerate.Null
}

// decodePolicy deliberately runs after the usecase's Captain authorization.
// Until then the optional fields retain raw JSON so a malformed value cannot
// change the required non-Captain response from 403 to 400.
func (r *createInviteRequest) decodePolicy() error {
	if err := r.ExpiresInHours.decode(); err != nil {
		return err
	}
	if err := r.MaxUses.decode(); err != nil {
		return err
	}
	return r.Regenerate.decode()
}

type optionalInt struct {
	Value int
	Set   bool
	Null  bool
	raw   json.RawMessage
}

func (v *optionalInt) UnmarshalJSON(data []byte) error {
	v.Value = 0
	v.Set = true
	v.Null = bytes.Equal(bytes.TrimSpace(data), []byte("null"))
	v.raw = append(v.raw[:0], data...)
	return nil
}

func (v *optionalInt) decode() error {
	if !v.Set || v.Null {
		return nil
	}
	return json.Unmarshal(v.raw, &v.Value)
}

type optionalBool struct {
	Value bool
	Set   bool
	Null  bool
	raw   json.RawMessage
}

func (v *optionalBool) UnmarshalJSON(data []byte) error {
	v.Value = false
	v.Set = true
	v.Null = bytes.Equal(bytes.TrimSpace(data), []byte("null"))
	v.raw = append(v.raw[:0], data...)
	return nil
}

func (v *optionalBool) decode() error {
	if !v.Set || v.Null {
		return nil
	}
	return json.Unmarshal(v.raw, &v.Value)
}

type joinGroupRequest struct {
	Code string `json:"code"`
}

type transferRoleRequest struct {
	Role string `json:"role"`
}

type renameGroupRequest struct {
	Name string `json:"name"`
}
