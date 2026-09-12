package dto

type OTPRequestRequest struct {
	Phone string `json:"phone"`
	// Channel: whatsapp | sms | auto. `auto` é o predefinido.
	Channel  string `json:"channel,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
}

// OTPRequestResponse é **exactamente a mesma** para número registado e
// desconhecido. Nunca diz se a conta existe.
type OTPRequestResponse struct {
	ChallengeID string `json:"challengeId"`
	ExpiresAt   string `json:"expiresAt"`
	ResendAfter int    `json:"resendAfter"`
}

type OTPVerifyRequest struct {
	// ⚠️ `challengeId` e não o número: se o verify aceitasse `{phone, code}`,
	// um atacante podia tentar códigos contra qualquer número sem nunca ter
	// pedido um. O desafio amarra a tentativa ao pedido.
	ChallengeID string `json:"challengeId"`
	Code        string `json:"code"`
	DeviceID    string `json:"deviceId,omitempty"`
	Platform    string `json:"platform,omitempty"`
}

type SessionResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	// IsNewUser só aparece depois de o código ser verificado — nessa altura a
	// pessoa já provou controlar o número.
	IsNewUser bool `json:"isNewUser,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
	DeviceID     string `json:"deviceId,omitempty"`
}
