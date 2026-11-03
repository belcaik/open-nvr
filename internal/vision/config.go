package vision

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/statefile"
)

// TrackingConfig configura el seguimiento PTZ futuro.
type TrackingConfig struct {
	Enabled          bool          `json:"enabled"`
	Speed            int           `json:"speed"`
	ZoomTargetHeight float64       `json:"zoom_target_height"`
	ReturnAfter      time.Duration `json:"return_after"`
}

// CameraConfig es la configuración de visión persistida para una cámara.
type CameraConfig struct {
	Enabled    bool           `json:"enabled"`
	Zones      []Zone         `json:"zones"`
	Tracking   TrackingConfig `json:"tracking"`
	HomePreset int            `json:"home_preset"`
}

// Store guarda configuraciones por número de cámara.
type Store struct {
	mu     sync.RWMutex
	path   string
	config map[int]CameraConfig
}

// NewStore carga el archivo existente o crea un almacén vacío.
func NewStore(path string) (*Store, error) {
	configs := make(map[int]CameraConfig)
	if _, err := statefile.Load(path, &configs); err != nil {
		return nil, fmt.Errorf("cargar configuración de visión: %w", err)
	}
	return &Store{path: path, config: configs}, nil
}

// Get devuelve una copia de la configuración y si la cámara existe.
func (s *Store) Get(cam int) (CameraConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.config[cam]
	return cloneConfig(cfg), ok
}

// Put valida, persiste y publica la configuración de una cámara.
func (s *Store) Put(cam int, cfg CameraConfig) error {
	if cam < 1 {
		return fmt.Errorf("cámara inválida: debe ser mayor o igual a 1")
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	cfg = cloneConfig(cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneConfigs(s.config)
	next[cam] = cfg
	if err := statefile.Save(s.path, next); err != nil {
		return fmt.Errorf("guardar configuración de visión: %w", err)
	}
	s.config = next
	return nil
}

// All devuelve una copia de todas las configuraciones.
func (s *Store) All() map[int]CameraConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfigs(s.config)
}

func validateConfig(cfg CameraConfig) error {
	if cfg.HomePreset < 0 {
		return fmt.Errorf("home preset inválido: debe ser mayor o igual a 0")
	}
	tracking := cfg.Tracking
	if tracking.Speed < 1 || tracking.Speed > 10 {
		return fmt.Errorf("velocidad de tracking inválida: debe estar entre 1 y 10")
	}
	if math.IsNaN(tracking.ZoomTargetHeight) || math.IsInf(tracking.ZoomTargetHeight, 0) || tracking.ZoomTargetHeight < 0.1 || tracking.ZoomTargetHeight > 0.9 {
		return fmt.Errorf("altura objetivo de zoom inválida: debe estar entre 0.1 y 0.9")
	}
	if tracking.ReturnAfter < 5*time.Second {
		return fmt.Errorf("retorno de tracking inválido: debe ser al menos 5s")
	}

	ids := make(map[string]struct{}, len(cfg.Zones))
	for i, zone := range cfg.Zones {
		if zone.ID == "" {
			return fmt.Errorf("zona %d: ID vacío", i)
		}
		if _, exists := ids[zone.ID]; exists {
			return fmt.Errorf("zona %d: ID duplicado %q", i, zone.ID)
		}
		ids[zone.ID] = struct{}{}
		if len(zone.Points) < 3 || len(zone.Points) > 32 {
			return fmt.Errorf("zona %q: el polígono debe tener entre 3 y 32 puntos", zone.ID)
		}
		if zone.Pose.Preset < 0 {
			return fmt.Errorf("zona %q: preset inválido", zone.ID)
		}
		for j, point := range zone.Points {
			if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) ||
				point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 {
				return fmt.Errorf("zona %q, punto %d: coordenadas fuera del intervalo [0,1]", zone.ID, j)
			}
		}
	}
	return nil
}

func cloneConfigs(configs map[int]CameraConfig) map[int]CameraConfig {
	copy := make(map[int]CameraConfig, len(configs))
	for cam, cfg := range configs {
		copy[cam] = cloneConfig(cfg)
	}
	return copy
}

func cloneConfig(cfg CameraConfig) CameraConfig {
	cfg.Zones = append([]Zone(nil), cfg.Zones...)
	for i := range cfg.Zones {
		cfg.Zones[i].Points = append([]Point(nil), cfg.Zones[i].Points...)
	}
	return cfg
}
