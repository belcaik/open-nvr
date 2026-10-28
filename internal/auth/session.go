package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const CookieName = "opennvr_session"

var errInvalid = errors.New("sesión no válida")

var b64url = base64.RawURLEncoding

// Session no se guarda en el servidor: viaja firmada en la cookie, así que
// sobrevive a reinicios mientras no cambie la clave (web_session_key).
type Session struct {
	User     string
	ID       string // aleatorio; de él sale el token CSRF
	Issued   time.Time
	LastSeen time.Time
}

type Sessions struct {
	Key     []byte
	Idle    time.Duration // caducidad por inactividad
	MaxAge  time.Duration // caducidad absoluta desde la emisión
	Refresh time.Duration // frecuencia máxima de renovación de la cookie
	Now     func() time.Time
}

func (s *Sessions) now() time.Time { return s.Now().Truncate(time.Second) }

func (s *Sessions) New(user string) (Session, error) {
	if user == "" || strings.Contains(user, "|") {
		return Session{}, errors.New("usuario no válido")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Session{}, err
	}
	now := s.now()
	return Session{User: user, ID: b64url.EncodeToString(id), Issued: now, LastSeen: now}, nil
}

func (s *Sessions) mac(data string) []byte {
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// Encode: base64url("v1|user|id|emisión|último uso") + "." + base64url(HMAC-SHA256).
func (s *Sessions) Encode(se Session) string {
	payload := strings.Join([]string{"v1", se.User, se.ID,
		strconv.FormatInt(se.Issued.Unix(), 10), strconv.FormatInt(se.LastSeen.Unix(), 10)}, "|")
	return b64url.EncodeToString([]byte(payload)) + "." + b64url.EncodeToString(s.mac(payload))
}

func (s *Sessions) Decode(v string) (Session, error) {
	enc, sig, ok := strings.Cut(v, ".")
	if !ok {
		return Session{}, errInvalid
	}
	payload, err1 := b64url.DecodeString(enc)
	mac, err2 := b64url.DecodeString(sig)
	if err1 != nil || err2 != nil || !hmac.Equal(mac, s.mac(string(payload))) {
		return Session{}, errInvalid
	}
	f := strings.Split(string(payload), "|")
	if len(f) != 5 || f[0] != "v1" || f[1] == "" || f[2] == "" {
		return Session{}, errInvalid
	}
	issued, err1 := strconv.ParseInt(f[3], 10, 64)
	last, err2 := strconv.ParseInt(f[4], 10, 64)
	if err1 != nil || err2 != nil {
		return Session{}, errInvalid
	}
	se := Session{User: f[1], ID: f[2], Issued: time.Unix(issued, 0), LastSeen: time.Unix(last, 0)}
	now := s.now()
	if now.Sub(se.LastSeen) > s.Idle || now.Sub(se.Issued) > s.MaxAge {
		return Session{}, errors.New("sesión caducada")
	}
	return se, nil
}

func (s *Sessions) NeedsRefresh(se Session) bool {
	return s.now().Sub(se.LastSeen) >= s.Refresh
}

// Touch devuelve la sesión con el último uso puesto a ahora.
func (s *Sessions) Touch(se Session) Session {
	se.LastSeen = s.now()
	return se
}

func (s *Sessions) Cookie(se Session) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: s.Encode(se), Path: "/", MaxAge: int(s.Idle.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}

func (s *Sessions) ClearCookie() *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}
