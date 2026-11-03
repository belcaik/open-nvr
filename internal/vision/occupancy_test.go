package vision

import (
	"testing"
	"time"
)

func occupancyZone() Zone {
	return Zone{ID: "door", Points: []Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1}}}
}

func frameWithTracks(ids ...int) Frame {
	frame := Frame{Cam: 1}
	for _, id := range ids {
		frame.Tracks = append(frame.Tracks, Track{ID: id, Box: Box{0.2, 0.2, 0.4, 0.8}})
	}
	return frame
}

func TestOccupancyEntersOnThirdFrame(t *testing.T) {
	var occupancy Occupancy
	now := time.Unix(100, 0)
	for i := 0; i < 2; i++ {
		if events := occupancy.Update(frameWithTracks(7), []Zone{occupancyZone()}, now.Add(time.Duration(i)*time.Millisecond)); len(events) != 0 {
			t.Fatalf("frame %d emitió %v", i+1, events)
		}
	}
	events := occupancy.Update(frameWithTracks(7), []Zone{occupancyZone()}, now.Add(2*time.Millisecond))
	if len(events) != 1 || events[0].Type != ZoneEnter || events[0].TrackID != 7 {
		t.Fatalf("tercer frame = %+v", events)
	}
}

func TestOccupancyOneFrameFlickerDoesNotLeave(t *testing.T) {
	var occupancy Occupancy
	now := time.Unix(100, 0)
	for i := 0; i < 3; i++ {
		occupancy.Update(frameWithTracks(7), []Zone{occupancyZone()}, now.Add(time.Duration(i)*time.Millisecond))
	}
	out := frameWithTracks(7)
	out.Tracks[0].Box = Box{1.1, 0.2, 1.2, 0.8}
	if events := occupancy.Update(out, []Zone{occupancyZone()}, now.Add(time.Second)); len(events) != 0 {
		t.Fatalf("parpadeo emitió %+v", events)
	}
	if events := occupancy.Update(frameWithTracks(7), []Zone{occupancyZone()}, now.Add(1100*time.Millisecond)); len(events) != 0 {
		t.Fatalf("regreso emitió %+v", events)
	}
}

func TestOccupancyLeavesAfterTwoSecondsMissing(t *testing.T) {
	var occupancy Occupancy
	now := time.Unix(100, 0)
	for i := 0; i < 3; i++ {
		occupancy.Update(frameWithTracks(7), []Zone{occupancyZone()}, now)
	}
	if events := occupancy.Update(frameWithTracks(), []Zone{occupancyZone()}, now.Add(1999*time.Millisecond)); len(events) != 0 {
		t.Fatalf("salida prematura: %+v", events)
	}
	events := occupancy.Update(frameWithTracks(), []Zone{occupancyZone()}, now.Add(2*time.Second))
	if len(events) != 1 || events[0].Type != ZoneLeave || events[0].TrackID != 7 {
		t.Fatalf("salida = %+v", events)
	}
}

func TestOccupancyTracksAreIndependent(t *testing.T) {
	var occupancy Occupancy
	now := time.Unix(100, 0)
	var events []ZoneEvent
	for i := 0; i < 3; i++ {
		events = occupancy.Update(frameWithTracks(7, 8), []Zone{occupancyZone()}, now)
	}
	if len(events) != 2 {
		t.Fatalf("eventos = %+v, quiero dos", events)
	}
	seen := map[int]bool{events[0].TrackID: true, events[1].TrackID: true}
	if !seen[7] || !seen[8] {
		t.Fatalf("tracks emitidos = %+v", events)
	}
}
