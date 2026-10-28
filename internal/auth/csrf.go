package auth

import "crypto/hmac"

// CSRFToken deriva el token de la sesión: no hace falta guardarlo.
func (s *Sessions) CSRFToken(se Session) string {
	return b64url.EncodeToString(s.mac("csrf|" + se.ID))
}

func (s *Sessions) CheckCSRF(se Session, token string) bool {
	return token != "" && hmac.Equal([]byte(token), []byte(s.CSRFToken(se)))
}
