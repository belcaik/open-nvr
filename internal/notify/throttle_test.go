package notify

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func newT(minInterval float64, maxPerHour int) (*Throttle, *ThrottleState) {
	st := &ThrottleState{}
	return NewThrottle(st, minInterval, maxPerHour), st
}

func TestDedupSameKeyWithinInterval(t *testing.T) {
	th, _ := newT(600, 100)
	got := []bool{th.Allow("a", 1000, -1), th.Allow("a", 1500, -1), th.Allow("a", 1600, -1)}
	if !reflect.DeepEqual(got, []bool{true, false, true}) {
		t.Fatal(got)
	}
}

func TestDifferentKeysIndependent(t *testing.T) {
	th, _ := newT(600, 100)
	if !th.Allow("a", 1000, -1) || !th.Allow("b", 1001, -1) {
		t.Fatal("claves distintas deben ser independientes")
	}
}

func TestCustomInterval(t *testing.T) {
	th, _ := newT(600, 100)
	got := []bool{th.Allow("a", 0, 86400), th.Allow("a", 3600, 86400), th.Allow("a", 86400, 86400)}
	if !reflect.DeepEqual(got, []bool{true, false, true}) {
		t.Fatal(got)
	}
}

func TestGlobalRateLimitAndSuppressedCount(t *testing.T) {
	th, _ := newT(0, 3)
	var got []bool
	for i := 0; i < 5; i++ {
		got = append(got, th.Allow(fmt.Sprintf("k%d", i), float64(100+i), -1))
	}
	if !reflect.DeepEqual(got, []bool{true, true, true, false, false}) {
		t.Fatal(got)
	}
	if th.TakeSuppressed() != 2 || th.TakeSuppressed() != 0 {
		t.Fatal("contador de suprimidos")
	}
	if !th.Allow("k9", 100+3600, -1) {
		t.Fatal("la ventana de una hora debía liberar")
	}
}

func TestForgetUndoesAllow(t *testing.T) {
	th, _ := newT(600, 1)
	th.Allow("a", 10, -1)
	th.Forget("a", 10)
	if !th.Allow("a", 11, -1) {
		t.Fatal("Forget no deshizo Allow")
	}
}

func TestStatePrunableAndRebuildable(t *testing.T) {
	th, st := newT(10, 10)
	th.Allow("old", 0, -1)
	th.Allow("new", 10*86400, -1)
	th.Prune(10*86400, 86400)
	if len(st.Last) != 1 || st.Last["new"] == 0 {
		t.Fatal(st.Last)
	}
	th2 := NewThrottle(st, 10, 10)
	if th2.Allow("new", 10*86400+1, -1) {
		t.Fatal("el estado reconstruido debía deduplicar")
	}
}

func TestThrottleStateJSONCompat(t *testing.T) {
	var st ThrottleState
	raw := `{"last":{"down:tcp:80":1793008800.5},"sent":[1793008800.5],"suppressed":2}`
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	th := NewThrottle(&st, 3600, 20)
	if th.Allow("down:tcp:80", 1793008801, -1) {
		t.Fatal("debía deduplicar con el estado de Python")
	}
	if n := th.TakeSuppressed(); n != 3 {
		t.Fatalf("suprimidos = %d", n)
	}
}

func TestEmptyStateSerializesLikePython(t *testing.T) {
	st := &ThrottleState{}
	NewThrottle(st, 1, 1)
	b, _ := json.Marshal(st)
	if string(b) != `{"last":{},"sent":[],"suppressed":0}` {
		t.Fatal(string(b))
	}
}
