package config

import (
	"reflect"
	"testing"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("X_STR", "  hola ")
	t.Setenv("X_INT", "42")
	t.Setenv("X_BOOL", "Sí")
	t.Setenv("X_LIST", " a, b ,,c ")
	t.Setenv("X_PORTS", "554,80,554")
	if got := Str("X_STR", "d"); got != "hola" {
		t.Fatalf("Str = %q", got)
	}
	if got := Str("X_MISSING", "d"); got != "d" {
		t.Fatalf("Str default = %q", got)
	}
	if got := Int("X_INT", 1); got != 42 {
		t.Fatalf("Int = %d", got)
	}
	if !Bool("X_BOOL", false) || Bool("X_MISSING", false) {
		t.Fatal("Bool")
	}
	if got := List("X_LIST", nil); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("List = %v", got)
	}
	if got := Ports("X_PORTS", nil); !reflect.DeepEqual(got, []int{80, 554}) {
		t.Fatalf("Ports = %v", got)
	}
	if got := Ports("X_MISSING", []int{9000, 80}); !reflect.DeepEqual(got, []int{80, 9000}) {
		t.Fatalf("Ports default = %v", got)
	}
}
