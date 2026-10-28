package camera

import (
	"context"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

// Service es lo que la web usa de las cámaras: lectura del NVR con caché
// corta y PTZ siempre a través del Guard.
type Service struct {
	NVR   *nvr.Client
	Guard *Guard

	mu       sync.Mutex
	cams     []nvr.Camera
	cachedAt time.Time
}

const camerasTTL = 30 * time.Second

func (s *Service) Cameras(ctx context.Context) ([]nvr.Camera, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cams != nil && time.Since(s.cachedAt) < camerasTTL {
		return s.cams, nil
	}
	cams, err := s.NVR.Cameras(ctx)
	if err != nil {
		return nil, err
	}
	s.cams, s.cachedAt = cams, time.Now()
	return cams, nil
}

func (s *Service) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := s.NVR.DeviceInfo(ctx)
	return err
}

func (s *Service) Snapshot(ctx context.Context, ch int) ([]byte, error) {
	return s.NVR.Snapshot(ctx, ch)
}

func (s *Service) PTZStart(ctx context.Context, ch int, cmd nvr.PTZCommand, speed int) error {
	return s.Guard.Start(ctx, ch, cmd, speed)
}

func (s *Service) PTZKeepalive(ch int) bool { return s.Guard.Keepalive(ch) }

func (s *Service) PTZStop(ctx context.Context, ch int) error { return s.Guard.Stop(ctx, ch) }

func (s *Service) Preset(ctx context.Context, ch int, action nvr.PresetAction, idx int) error {
	return s.NVR.Preset(ctx, ch, action, idx)
}

func (s *Service) Light(ctx context.Context, ch int, on bool) error { return s.NVR.Light(ctx, ch, on) }

func (s *Service) Wiper(ctx context.Context, ch int, on bool) error { return s.NVR.Wiper(ctx, ch, on) }
