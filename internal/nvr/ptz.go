package nvr

import (
	"context"
	"fmt"
	"net/http"
)

// PTZCommand es un movimiento continuo que se envía a /PTZ/{ch}/{cmd}.
// Solo se admiten los que ofrece la interfaz web propia; el NVR conoce más
// (AutoLeftRight, ApertureBigger...) que no se exponen.
type PTZCommand string

const (
	TurnUp        PTZCommand = "TurnUp"
	TurnDown      PTZCommand = "TurnDown"
	TurnLeft      PTZCommand = "TurnLeft"
	TurnRight     PTZCommand = "TurnRight"
	TurnUpLeft    PTZCommand = "TurnUpLeft"
	TurnUpRight   PTZCommand = "TurnUpRight"
	TurnDownLeft  PTZCommand = "TurnDownLeft"
	TurnDownRight PTZCommand = "TurnDownRight"
	ZoomIn        PTZCommand = "ZoomIn"
	ZoomOut       PTZCommand = "ZoomOut"
	FocusNear     PTZCommand = "FocusNear"
	FocusFar      PTZCommand = "FocusFar"
)

// ValidPTZCommand devuelve el comando si es uno de los admitidos.
func ValidPTZCommand(s string) (PTZCommand, bool) {
	switch PTZCommand(s) {
	case TurnUp, TurnDown, TurnLeft, TurnRight,
		TurnUpLeft, TurnUpRight, TurnDownLeft, TurnDownRight,
		ZoomIn, ZoomOut, FocusNear, FocusFar:
		return PTZCommand(s), true
	}
	return "", false
}

// PTZ mueve la cámara: PUT /PTZ/{ch}/{cmd} con el cuerpo
// "Param1=1&Param2={speed}" (arranque) o "Param1=0&Param2={speed}" (parada),
// igual que la UI original en mousedown/mouseup. speed se limita a 1..10.
func (c *Client) PTZ(ctx context.Context, ch int, cmd PTZCommand, on bool, speed int) error {
	if speed < 1 {
		speed = 1
	}
	if speed > 10 {
		speed = 10
	}
	p1 := 0
	if on {
		p1 = 1
	}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/PTZ/%d/%s", ch, cmd),
		fmt.Sprintf("Param1=%d&Param2=%d", p1, speed), controlTimeout)
	return err
}

// PresetAction es una operación sobre los presets de una cámara.
type PresetAction string

const (
	PresetGoto   PresetAction = "Goto"
	PresetSet    PresetAction = "Set"
	PresetRemove PresetAction = "Remove"
)

// Preset ejecuta una acción de preset: PUT /PTZ/{ch}/Presets/{action} con
// cuerpo "Param1={idx}". El índice es cero-based (0..255), como en el NVR;
// la interfaz web muestra 1..256 y resta uno antes de llamar.
func (c *Client) Preset(ctx context.Context, ch int, action PresetAction, idx int) error {
	if idx < 0 || idx > 255 {
		return fmt.Errorf("nvr: índice de preset %d fuera de rango (0..255)", idx)
	}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/PTZ/%d/Presets/%s", ch, action),
		fmt.Sprintf("Param1=%d", idx), controlTimeout)
	return err
}

// Light enciende o apaga la luz blanca: PUT /PTZ/{ch}/Light, Param1=1|0.
func (c *Client) Light(ctx context.Context, ch int, on bool) error {
	return c.toggle(ctx, ch, "Light", on)
}

// Wiper acciona el limpiaparabrisas: PUT /PTZ/{ch}/RainBrush, Param1=1|0.
func (c *Client) Wiper(ctx context.Context, ch int, on bool) error {
	return c.toggle(ctx, ch, "RainBrush", on)
}

func (c *Client) toggle(ctx context.Context, ch int, what string, on bool) error {
	p1 := 0
	if on {
		p1 = 1
	}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/PTZ/%d/%s", ch, what),
		fmt.Sprintf("Param1=%d", p1), controlTimeout)
	return err
}
