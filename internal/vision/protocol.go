// Package vision procesa las detecciones y zonas de las cámaras.
package vision

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Box contiene x1, y1, x2, y2 en coordenadas normalizadas.
type Box [4]float64

// Track es una persona seguida por el detector.
type Track struct {
	ID   int     `json:"id"`
	Conf float64 `json:"conf"`
	Box  Box     `json:"box"`
}

// Frame agrupa las detecciones de una cámara en un instante.
type Frame struct {
	Type   string    `json:"type"`
	Cam    int       `json:"cam"`
	Seq    uint64    `json:"seq"`
	TS     time.Time `json:"ts"`
	W      int       `json:"w"`
	H      int       `json:"h"`
	Tracks []Track   `json:"tracks"`
}

// Status describe el estado del detector para una cámara.
type Status struct {
	Type       string  `json:"type"`
	Cam        int     `json:"cam"`
	State      string  `json:"state"`
	FPS        float64 `json:"fps"`
	InferMSP50 float64 `json:"infer_ms_p50"`
	Dropped    uint64  `json:"dropped"`
}

// Decode interpreta un mensaje del detector. Los tipos desconocidos se ignoran.
func Decode(data []byte) (any, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("decodificar mensaje de visión: %w", err)
	}

	switch head.Type {
	case "detections":
		var frame Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			return nil, fmt.Errorf("decodificar detecciones: %w", err)
		}
		if frame.Cam < 1 {
			return nil, fmt.Errorf("cámara inválida: debe ser mayor o igual a 1")
		}
		for i, track := range frame.Tracks {
			if err := validateBox(track.Box); err != nil {
				return nil, fmt.Errorf("track %d: %w", i, err)
			}
		}
		return frame, nil
	case "status":
		var status Status
		if err := json.Unmarshal(data, &status); err != nil {
			return nil, fmt.Errorf("decodificar estado: %w", err)
		}
		if status.Cam < 1 {
			return nil, fmt.Errorf("cámara inválida: debe ser mayor o igual a 1")
		}
		switch status.State {
		case "starting", "ok", "reconnecting":
		default:
			return nil, fmt.Errorf("estado inválido: %q", status.State)
		}
		return status, nil
	default:
		return nil, nil
	}
}

func validateBox(box Box) error {
	for _, coordinate := range box {
		if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) || coordinate < 0 || coordinate > 1 {
			return fmt.Errorf("caja fuera del intervalo [0,1]")
		}
	}
	if box[0] >= box[2] || box[1] >= box[3] {
		return fmt.Errorf("caja invertida o vacía")
	}
	return nil
}
