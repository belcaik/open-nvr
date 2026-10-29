package mpegps

import (
	"errors"
	"fmt"
)

var (
	ErrBufferLimit  = errors.New("límite de búfer MPEG-PS")
	ErrPacketLimit  = errors.New("límite de paquete MPEG-PS")
	ErrInvalidLimit = errors.New("límite MPEG-PS inválido")
)

// LimitError informa del límite superado sin incluir bytes del stream.
type LimitError struct {
	Field  string
	Length int
	Limit  int
	Err    error
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("mpegps: %s: %d bytes superan el límite %d", e.Field, e.Length, e.Limit)
}

func (e *LimitError) Unwrap() error { return e.Err }
