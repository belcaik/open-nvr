package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/belcaik/open-nvr/internal/nvr"
)

// nvrError traduce los errores del cliente a código HTTP y mensaje.
func nvrError(err error) (int, string) {
	var bad *nvr.BadResponseError
	switch {
	case errors.Is(err, nvr.ErrUnavailable):
		return http.StatusServiceUnavailable, "El NVR no responde"
	case errors.Is(err, nvr.ErrUnauthorized):
		return http.StatusBadGateway, "El NVR rechazó las credenciales"
	case errors.As(err, &bad):
		if bad.Status != "" {
			return http.StatusBadGateway, "El NVR devolvió statusCode " + bad.Status
		}
		return http.StatusBadGateway, fmt.Sprintf("El NVR devolvió HTTP %d", bad.HTTP)
	}
	return http.StatusBadGateway, "Error del NVR"
}

func (s *server) ptzStart(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	cmd, ok := nvr.ValidPTZCommand(r.PostFormValue("cmd"))
	speed, err := strconv.Atoi(r.PostFormValue("speed"))
	if !ok || err != nil || speed < 1 || speed > 10 {
		http.Error(w, "Comando o velocidad no válidos", http.StatusBadRequest)
		return
	}
	if err := s.d.Cameras.PTZStart(r.Context(), ch, cmd, speed); err != nil {
		code, msg := nvrError(err)
		http.Error(w, msg, code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ptzKeepalive(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.d.Cameras.PTZKeepalive(ch) {
		http.Error(w, "Sin movimiento activo", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ptzStop(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.d.Cameras.PTZStop(r.Context(), ch); err != nil {
		code, msg := nvrError(err)
		http.Error(w, msg, code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var presetActions = map[string]nvr.PresetAction{
	"goto": nvr.PresetGoto, "set": nvr.PresetSet, "remove": nvr.PresetRemove,
}

var presetDone = map[nvr.PresetAction]string{
	nvr.PresetGoto: "Yendo al preset %d", nvr.PresetSet: "Preset %d guardado", nvr.PresetRemove: "Preset %d borrado",
}

// result devuelve el parcial que htmx pone en el <output> del panel.
func (s *server) result(w http.ResponseWriter, r *http.Request, err error, okMsg string) {
	if err != nil {
		code, msg := nvrError(err)
		s.render(w, r, code, "_result", "", struct {
			OK  bool
			Msg string
		}{false, msg})
		return
	}
	s.render(w, r, http.StatusOK, "_result", "", struct {
		OK  bool
		Msg string
	}{true, okMsg})
}

func (s *server) ptzPreset(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	action, okA := presetActions[r.PathValue("action")]
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if !ok || !okA || err != nil || idx < 1 || idx > 256 {
		http.Error(w, "Preset no válido (1–256)", http.StatusBadRequest)
		return
	}
	// La UI numera desde 1 y el NVR desde 0.
	err = s.d.Cameras.Preset(r.Context(), ch, action, idx-1)
	s.result(w, r, err, fmt.Sprintf(presetDone[action], idx))
}

func (s *server) ptzSwitch(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	state := r.PathValue("state")
	if !ok || (state != "on" && state != "off") {
		http.NotFound(w, r)
		return
	}
	on := state == "on"
	var err error
	var label string
	switch r.PathValue("device") {
	case "light":
		err = s.d.Cameras.Light(r.Context(), ch, on)
		label = map[bool]string{true: "Luz encendida", false: "Luz apagada"}[on]
	case "wiper":
		err = s.d.Cameras.Wiper(r.Context(), ch, on)
		label = map[bool]string{true: "Limpiaparabrisas encendido", false: "Limpiaparabrisas apagado"}[on]
	default:
		http.NotFound(w, r)
		return
	}
	s.result(w, r, err, label)
}
