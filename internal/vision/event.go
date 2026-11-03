package vision

import "time"

// EventKind es el tipo de un evento de visión.
type EventKind string

const (
	EventZoneEnter       EventKind = "zone_enter"
	EventZoneLeave       EventKind = "zone_leave"
	EventTrackingStart   EventKind = "tracking_start"
	EventTrackingStop    EventKind = "tracking_stop"
	EventCameraDisplaced EventKind = "camera_displaced"
	EventDetectorDown    EventKind = "detector_down"
)

// Event es lo que el motor publica en el bus: lo consumen el historial,
// la web (SSE) y los sinks de notificación. Snapshot lo rellena el
// historial con la ruta relativa de la instantánea, si la hay.
type Event struct {
	ID       string    `json:"id"`
	Kind     EventKind `json:"kind"`
	Cam      int       `json:"cam"`
	ZoneID   string    `json:"zone_id,omitempty"`
	TrackID  int       `json:"track_id,omitempty"`
	At       time.Time `json:"at"`
	Box      *Box      `json:"box,omitempty"`
	Message  string    `json:"message,omitempty"`
	Snapshot string    `json:"snapshot,omitempty"`
}
