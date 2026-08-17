package http

import "encoding/json"

type signUpRequest struct {
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}
type tokenRequest struct {
	Token string `json:"token"`
}
type emailRequest struct {
	Email string `json:"email"`
}
type signInRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	FCMToken   string `json:"fcm_token,omitempty"`
}
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}
type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type optionalString struct {
	Set   bool
	Value *string
}

func (o *optionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type patchProfileRequest struct {
	DisplayName       optionalString `json:"display_name"`
	PhoneNumber       optionalString `json:"phone_number"`
	BankCode          optionalString `json:"bank_code"`
	BankAccountNumber optionalString `json:"bank_account_number"`
	BankAccountHolder optionalString `json:"bank_account_holder"`
}

type updateFCMTokenRequest struct {
	FCMToken string `json:"fcm_token"`
}

