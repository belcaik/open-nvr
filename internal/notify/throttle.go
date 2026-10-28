// Package notify deduplica avisos y los envía a ntfy.
package notify

// ThrottleState se serializa igual que el dict de la versión Python:
// {"last": {clave: epoch}, "sent": [epoch...], "suppressed": n}.
type ThrottleState struct {
	Last       map[string]float64 `json:"last"`
	Sent       []float64          `json:"sent"`
	Suppressed int                `json:"suppressed"`
}

// Throttle aplica dos reglas: la misma clave no se repite antes de su
// intervalo y no salen más de MaxPerHour avisos en una hora. Los
// descartados se cuentan para mencionarlos en el siguiente aviso.
// Lógica pura: el tiempo llega como argumento.
type Throttle struct {
	State       *ThrottleState
	MinInterval float64
	MaxPerHour  int
}

func NewThrottle(st *ThrottleState, minInterval float64, maxPerHour int) *Throttle {
	if st.Last == nil {
		st.Last = map[string]float64{}
	}
	if st.Sent == nil {
		st.Sent = []float64{}
	}
	return &Throttle{State: st, MinInterval: minInterval, MaxPerHour: maxPerHour}
}

// Allow registra el envío si está permitido. interval < 0 usa MinInterval.
func (t *Throttle) Allow(key string, now, interval float64) bool {
	if interval < 0 {
		interval = t.MinInterval
	}
	st := t.State
	if last, ok := st.Last[key]; ok && now-last < interval {
		st.Suppressed++
		return false
	}
	recent := []float64{}
	for _, ts := range st.Sent {
		if now-ts < 3600 {
			recent = append(recent, ts)
		}
	}
	if len(recent) >= t.MaxPerHour {
		st.Sent = recent
		st.Suppressed++
		return false
	}
	st.Sent = append(recent, now)
	st.Last[key] = now
	return true
}

// Forget deshace el registro de Allow cuando el envío ha fallado.
func (t *Throttle) Forget(key string, now float64) {
	delete(t.State.Last, key)
	for i, ts := range t.State.Sent {
		if ts == now {
			t.State.Sent = append(t.State.Sent[:i], t.State.Sent[i+1:]...)
			break
		}
	}
}

func (t *Throttle) TakeSuppressed() int {
	n := t.State.Suppressed
	t.State.Suppressed = 0
	return n
}

// Prune olvida claves antiguas para que el estado no crezca sin límite.
func (t *Throttle) Prune(now, maxAge float64) {
	for k, ts := range t.State.Last {
		if now-ts > maxAge {
			delete(t.State.Last, k)
		}
	}
}
