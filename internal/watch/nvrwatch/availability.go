package nvrwatch

import (
	"fmt"
	"slices"
)

const (
	Down = "down"
	Up   = "up"
)

type AvailabilityState struct {
	Fails int      `json:"fails"`
	Down  bool     `json:"down"`
	Since *float64 `json:"since"`
}

type Event struct {
	Kind     string
	Name     string
	Detail   string
	Duration *float64
}

type Availability struct {
	State     map[string]*AvailabilityState
	Threshold int
}

func NewAvailability(state map[string]*AvailabilityState, threshold int) *Availability {
	return &Availability{State: state, Threshold: max(1, threshold)}
}

// Update aplica un resultado. Un threshold <= 0 usa el configurado.
func (a *Availability) Update(name string, ok bool, now float64, detail string, threshold int) *Event {
	if threshold <= 0 {
		threshold = a.Threshold
	}
	threshold = max(1, threshold)
	state := a.State[name]
	if state == nil {
		state = &AvailabilityState{}
		a.State[name] = state
	}
	if ok {
		wasDown, since := state.Down, state.Since
		state.Fails, state.Down, state.Since = 0, false, nil
		if wasDown {
			var duration *float64
			if since != nil {
				value := now - *since
				duration = &value
			}
			return &Event{Kind: Up, Name: name, Detail: detail, Duration: duration}
		}
		return nil
	}
	state.Fails++
	if state.Since == nil {
		value := now
		state.Since = &value
	}
	if !state.Down && state.Fails >= threshold {
		state.Down = true
		return &Event{Kind: Down, Name: name, Detail: detail}
	}
	return nil
}

func (a *Availability) DownNames() []string {
	var names []string
	for name, state := range a.State {
		if state.Down {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func formatDuration(seconds *float64) string {
	if seconds == nil {
		return "?"
	}
	total := int(*seconds)
	hours, remainder := total/3600, total%3600
	minutes, secs := remainder/60, remainder%60
	if hours > 0 {
		return fmt.Sprintf("%d h %d min", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d min %d s", minutes, secs)
	}
	return fmt.Sprintf("%d s", secs)
}
