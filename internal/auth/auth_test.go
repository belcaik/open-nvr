package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	h, err := HashPassword("correcta")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatal(h)
	}
	if !VerifyPassword(h, "correcta") || VerifyPassword(h, "incorrecta") {
		t.Fatal("verificación")
	}
	if VerifyPassword("$argon2id$roto", "correcta") || VerifyPassword("", "") {
		t.Fatal("hash corrupto debe dar false")
	}
	h2, _ := HashPassword("correcta")
	if h == h2 {
		t.Fatal("la sal debe variar")
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newSessions(key string) (*Sessions, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	return &Sessions{Key: []byte(strings.Repeat(key, 32)), Idle: 7 * 24 * time.Hour,
		MaxAge: 30 * 24 * time.Hour, Refresh: time.Hour, Now: c.now}, c
}

func TestSessionRoundTrip(t *testing.T) {
	s, _ := newSessions("k")
	se, err := s.New("admin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Decode(s.Encode(se))
	if err != nil || got != se {
		t.Fatalf("%v %+v %+v", err, got, se)
	}
	if _, err := s.New("ad|min"); err == nil {
		t.Fatal("usuario con | debe rechazarse")
	}
}

func TestSessionTamper(t *testing.T) {
	s, _ := newSessions("k")
	se, _ := s.New("admin")
	v := s.Encode(se)
	for _, i := range []int{0, len(v) - 1} {
		b := []byte(v)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		if _, err := s.Decode(string(b)); err == nil {
			t.Fatalf("modificación en %d aceptada", i)
		}
	}
	other, _ := newSessions("x")
	if _, err := other.Decode(v); err == nil {
		t.Fatal("otra clave aceptada")
	}
	for _, bad := range []string{"", ".", "abc", "a.b.c"} {
		if _, err := s.Decode(bad); err == nil {
			t.Fatalf("%q aceptado", bad)
		}
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	s, c := newSessions("k")
	se, _ := s.New("admin")
	v := s.Encode(se)
	c.t = c.t.Add(7*24*time.Hour + time.Second)
	if _, err := s.Decode(v); err == nil {
		t.Fatal("sesión inactiva aceptada")
	}
}

func TestSessionMaxAge(t *testing.T) {
	s, c := newSessions("k")
	se, _ := s.New("admin")
	c.t = c.t.Add(30*24*time.Hour + time.Second)
	se.LastSeen = c.t // uso reciente
	if _, err := s.Decode(s.Encode(se)); err == nil {
		t.Fatal("sesión más antigua que MaxAge aceptada")
	}
}

func TestSessionSurvivesNewInstance(t *testing.T) {
	a, _ := newSessions("k")
	se, _ := a.New("admin")
	b, _ := newSessions("k") // reinicio del proceso con el mismo secreto
	if _, err := b.Decode(a.Encode(se)); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsRefresh(t *testing.T) {
	s, c := newSessions("k")
	se, _ := s.New("admin")
	c.t = c.t.Add(59 * time.Minute)
	if s.NeedsRefresh(se) {
		t.Fatal("59 min no debe refrescar")
	}
	c.t = c.t.Add(2 * time.Minute)
	if !s.NeedsRefresh(se) {
		t.Fatal("61 min debe refrescar")
	}
}

func TestCookieFlags(t *testing.T) {
	s, _ := newSessions("k")
	se, _ := s.New("admin")
	ck := s.Cookie(se)
	if ck.Name != CookieName || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" {
		t.Fatalf("%+v", ck)
	}
	if ck.MaxAge != int((7 * 24 * time.Hour).Seconds()) {
		t.Fatal(ck.MaxAge)
	}
	if cl := s.ClearCookie(); cl.MaxAge >= 0 || cl.Name != CookieName {
		t.Fatalf("%+v", cl)
	}
}

func TestCSRF(t *testing.T) {
	s, _ := newSessions("k")
	a, _ := s.New("admin")
	b, _ := s.New("admin")
	tok := s.CSRFToken(a)
	if !s.CheckCSRF(a, tok) || s.CheckCSRF(b, tok) || s.CheckCSRF(a, "") {
		t.Fatal("CSRF")
	}
}

func TestLimiter(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(5, 15*time.Minute, c.now)
	for i := 0; i < 5; i++ {
		if l.Blocked("1.2.3.4") {
			t.Fatalf("bloqueado tras %d fallos", i)
		}
		l.Fail("1.2.3.4")
	}
	if !l.Blocked("1.2.3.4") || l.Blocked("5.6.7.8") {
		t.Fatal("debía bloquear solo esa IP")
	}
	c.t = c.t.Add(15*time.Minute + time.Second)
	if l.Blocked("1.2.3.4") {
		t.Fatal("la ventana debía expirar")
	}
	for i := 0; i < 5; i++ {
		l.Fail("1.2.3.4")
	}
	l.Reset("1.2.3.4")
	if l.Blocked("1.2.3.4") {
		t.Fatal("Reset")
	}
}
