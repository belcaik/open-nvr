package pose

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
)

// Grabber obtiene el JPEG actual de una cámara.
type Grabber interface {
	Frame(ctx context.Context, cam int) ([]byte, error)
}

// Go2RTC obtiene frames de la API HTTP de go2rtc.
type Go2RTC struct {
	Base   string
	HTTP   *http.Client
	Stream func(cam int) string
}

// Frame obtiene y valida un frame JPEG.
func (g *Go2RTC) Frame(ctx context.Context, cam int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stream := fmt.Sprintf("cam%d_sub", cam)
	if g.Stream != nil {
		stream = g.Stream(cam)
	}
	endpoint := strings.TrimRight(g.Base, "/") + "/api/frame.jpeg?src=" + url.QueryEscape(stream)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("pose: crear petición de frame: %w", err)
	}
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pose: obtener frame: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pose: obtener frame: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pose: leer frame: %w", err)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("pose: respuesta no es JPEG: %w", err)
	}
	return data, nil
}

// Check contiene la referencia disponible y su comparación.
type Check struct {
	HasReference bool
	Mode         Mode
	Result
}

// Verifier captura y compara referencias visuales.
type Verifier struct {
	Grab Grabber
	Refs *RefStore
}

// Capture guarda el frame actual como referencia del modo detectado.
func (v *Verifier) Capture(ctx context.Context, cam, preset int) (Mode, error) {
	g, mode, err := v.frame(ctx, cam)
	if err != nil {
		return Day, err
	}
	if err := v.Refs.Save(cam, preset, mode, g); err != nil {
		return mode, err
	}
	logx.Printf("pose: referencia guardada para cámara %d, preset %d, modo %s", cam, preset, mode)
	return mode, nil
}

// Verify compara el frame actual con la referencia de su modo.
func (v *Verifier) Verify(ctx context.Context, cam, preset int) (Check, error) {
	cur, mode, err := v.frame(ctx, cam)
	if err != nil {
		return Check{}, err
	}
	check := Check{Mode: mode}
	ref, ok, err := v.Refs.Load(cam, preset, mode)
	if err != nil {
		return Check{}, err
	}
	if !ok {
		return check, nil
	}
	check.HasReference = true
	check.Result = Compare(ref, cur)
	logx.Printf("pose: cámara %d, preset %d: dx=%d dy=%d cost=%.2f verificado=%t", cam, preset, check.Dx, check.Dy, check.Cost, check.Verified)
	// Con alineación exacta la vista actual sustituye a la referencia, así
	// sigue los cambios lentos de luz sin acumular deriva.
	if check.Verified && check.Dx == 0 && check.Dy == 0 {
		if err := v.Refs.Save(cam, preset, mode, cur); err != nil {
			logx.Printf("pose: renovar referencia de cámara %d, preset %d: %v", cam, preset, err)
		}
	}
	return check, nil
}

func (v *Verifier) frame(ctx context.Context, cam int) (Gray, Mode, error) {
	data, err := v.Grab.Frame(ctx, cam)
	if err != nil {
		return Gray{}, Day, err
	}
	g, mode, err := FromJPEG(data)
	if err != nil {
		return Gray{}, Day, err
	}
	return g, mode, nil
}
