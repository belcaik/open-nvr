package vision

import "time"

const (
	defaultEnterFrames = 3
	defaultLeaveAfter  = 2 * time.Second
)

// ZoneEventType identifica una entrada o salida de zona.
type ZoneEventType string

const (
	ZoneEnter ZoneEventType = "enter"
	ZoneLeave ZoneEventType = "leave"
)

// ZoneEvent representa el cambio de un track en una zona.
type ZoneEvent struct {
	Type    ZoneEventType
	Cam     int
	ZoneID  string
	TrackID int
	At      time.Time
	Box     Box
}

type occupancyKey struct {
	zoneID  string
	trackID int
}

type occupancyState struct {
	hits    int
	inside  bool
	lastHit time.Time
	lastBox Box
}

// Occupancy aplica antirrebote a los cruces de zona de una cámara.
type Occupancy struct {
	EnterFrames int
	LeaveAfter  time.Duration

	states map[occupancyKey]*occupancyState
}

// Update incorpora un frame y devuelve solo los cambios confirmados.
func (o *Occupancy) Update(frame Frame, zones []Zone, now time.Time) []ZoneEvent {
	if o.states == nil {
		o.states = make(map[occupancyKey]*occupancyState)
	}
	enterFrames := o.EnterFrames
	if enterFrames <= 0 {
		enterFrames = defaultEnterFrames
	}
	leaveAfter := o.LeaveAfter
	if leaveAfter <= 0 {
		leaveAfter = defaultLeaveAfter
	}

	hit := make(map[occupancyKey]bool)
	seen := make(map[int]Box, len(frame.Tracks))
	var events []ZoneEvent
	for _, track := range frame.Tracks {
		seen[track.ID] = track.Box
		for _, zone := range zones {
			key := occupancyKey{zoneID: zone.ID, trackID: track.ID}
			if !zone.Hit(track.Box) {
				continue
			}
			hit[key] = true
			state := o.states[key]
			if state == nil {
				state = &occupancyState{}
				o.states[key] = state
			}
			state.lastHit = now
			state.lastBox = track.Box
			if state.inside {
				continue
			}
			state.hits++
			if state.hits >= enterFrames {
				state.inside = true
				events = append(events, ZoneEvent{Type: ZoneEnter, Cam: frame.Cam, ZoneID: zone.ID, TrackID: track.ID, At: now, Box: track.Box})
			}
		}
	}

	for key, state := range o.states {
		if hit[key] {
			continue
		}
		if box, ok := seen[key.trackID]; ok {
			state.lastBox = box
		}
		if !state.inside {
			delete(o.states, key)
			continue
		}
		if now.Sub(state.lastHit) >= leaveAfter {
			events = append(events, ZoneEvent{Type: ZoneLeave, Cam: frame.Cam, ZoneID: key.zoneID, TrackID: key.trackID, At: now, Box: state.lastBox})
			delete(o.states, key)
		}
	}
	return events
}
