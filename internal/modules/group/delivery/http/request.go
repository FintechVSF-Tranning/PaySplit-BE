package http

type createGroupRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency,omitempty"`
}

type createInviteRequest struct {
	ExpiresInHours *int `json:"expires_in_hours,omitempty"`
	MaxUses        *int `json:"max_uses,omitempty"`
	Regenerate     bool `json:"regenerate,omitempty"`
}

type joinGroupRequest struct {
	Code string `json:"code"`
}

type transferRoleRequest struct {
	Role string `json:"role"`
}
