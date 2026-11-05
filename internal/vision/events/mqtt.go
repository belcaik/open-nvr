package events

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/vision"
)

// Publisher publica un mensaje MQTT. El cliente real vive fuera de este paquete.
type Publisher interface {
	Publish(topic string, payload []byte, retain bool) error
}

// CameraInfo describe una cámara conocida por la app.
type CameraInfo struct {
	Cam    int
	Name   string
	Online *bool // nil = desconocido
}

// MQTTSink publica presencia, eventos y discovery de Home Assistant.
type MQTTSink struct {
	Pub         Publisher
	Prefix      string // "opennvr"
	Discovery   string // "homeassistant"
	Cameras     func() []CameraInfo
	Zones       func(cam int) []vision.Zone
	SnapshotURL func(rel string) string

	mu   sync.Mutex
	cams map[int]*camState
}

// haID fija la identidad de dispositivos y entidades en Home Assistant: no
// depende de Prefix para que cambiar los topics no duplique entidades.
const haID = "opennvr"

type camState struct {
	occupied       map[string]map[int]struct{} // zona → tracks dentro
	tracking       bool
	connected      *bool
	snapshotURL    string
	zids           map[string]string
	usedZids       map[string]string
	publishedZones map[string]string
	lastPerson     string
	lastZones      map[string]string
	lastConnected  *bool
	lastSnapshot   string
}

var cameraEventTypes = []string{
	string(vision.EventZoneEnter),
	string(vision.EventZoneLeave),
	string(vision.EventTrackingStart),
	string(vision.EventTrackingStop),
	string(vision.EventCameraDisplaced),
	string(vision.EventCameraOffline),
	string(vision.EventCameraOnline),
}

type haEvent struct {
	EventType   string `json:"event_type"`
	ID          string `json:"id,omitempty"`
	ZoneID      string `json:"zone_id,omitempty"`
	ZoneName    string `json:"zone_name,omitempty"`
	TrackID     int    `json:"track_id,omitempty"`
	At          string `json:"at,omitempty"`
	Message     string `json:"message,omitempty"`
	SnapshotURL string `json:"snapshot_url,omitempty"`
}

func (s *MQTTSink) prefix() string {
	if s.Prefix == "" {
		return "opennvr"
	}
	return s.Prefix
}

func (s *MQTTSink) discoveryPrefix() string {
	if s.Discovery == "" {
		return "homeassistant"
	}
	return s.Discovery
}

func (s *MQTTSink) statusTopic() string {
	return s.prefix() + "/status"
}

func (s *MQTTSink) camNode(cam int) string {
	return fmt.Sprintf("%s_cam%d", haID, cam)
}

func (s *MQTTSink) camTopic(cam int, rest string) string {
	return fmt.Sprintf("%s/cam%d/%s", s.prefix(), cam, rest)
}

func (s *MQTTSink) configTopic(component, node, object string) string {
	return fmt.Sprintf("%s/%s/%s/%s/config", s.discoveryPrefix(), component, node, object)
}

func (s *MQTTSink) origin() map[string]any {
	return map[string]any{"name": "open-nvr"}
}

func (s *MQTTSink) hubDevice() map[string]any {
	return map[string]any{
		"identifiers":  []string{haID},
		"manufacturer": "open-nvr",
		"name":         "open-nvr",
	}
}

func (s *MQTTSink) camDevice(info CameraInfo) map[string]any {
	return map[string]any{
		"identifiers": []string{s.camNode(info.Cam)},
		"name":        cameraName(info),
		"via_device":  haID,
	}
}

func cameraName(info CameraInfo) string {
	if info.Name != "" {
		return info.Name
	}
	return fmt.Sprintf("Cámara %d", info.Cam)
}

func (s *MQTTSink) state(cam int) *camState {
	if s.cams == nil {
		s.cams = map[int]*camState{}
	}
	st := s.cams[cam]
	if st == nil {
		st = &camState{
			occupied:       map[string]map[int]struct{}{},
			zids:           map[string]string{},
			usedZids:       map[string]string{},
			publishedZones: map[string]string{},
			lastZones:      map[string]string{},
		}
		s.cams[cam] = st
	}
	return st
}

// Send actualiza el estado interno y publica solo los topics que cambian.
func (s *MQTTSink) Send(_ context.Context, ev vision.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Kind == vision.EventDetectorDown {
		return s.publishEventJSON(ev, 0)
	}
	st := s.state(ev.Cam)
	s.ensureZids(ev.Cam, st)
	switch ev.Kind {
	case vision.EventZoneEnter:
		// Occupancy emite entradas y salidas por track: la zona sigue
		// ocupada mientras quede alguno dentro.
		if st.occupied[ev.ZoneID] == nil {
			st.occupied[ev.ZoneID] = map[int]struct{}{}
		}
		st.occupied[ev.ZoneID][ev.TrackID] = struct{}{}
	case vision.EventZoneLeave:
		delete(st.occupied[ev.ZoneID], ev.TrackID)
		if len(st.occupied[ev.ZoneID]) == 0 {
			delete(st.occupied, ev.ZoneID)
		}
	case vision.EventTrackingStart:
		st.tracking = true
	case vision.EventTrackingStop:
		st.tracking = false
	case vision.EventCameraOffline:
		st.connected = boolPtr(false)
		st.occupied = map[string]map[int]struct{}{}
		st.tracking = false
	case vision.EventCameraOnline:
		st.connected = boolPtr(true)
	}
	if ev.Snapshot != "" && s.SnapshotURL != nil {
		if url := s.SnapshotURL(ev.Snapshot); url != "" {
			st.snapshotURL = url
		}
	}
	err := s.publishCamState(ev.Cam, st, CameraInfo{Cam: ev.Cam}, false)
	return firstErr(err, s.publishEventJSON(ev, ev.Cam))
}

// Resync publica discovery y el estado completo. Es idempotente.
func (s *MQTTSink) Resync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resyncLocked()
}

// HAStatus republica discovery y estado cuando Home Assistant vuelve online.
func (s *MQTTSink) HAStatus(payload []byte) {
	if string(bytes.TrimSpace(payload)) == "online" {
		s.Resync()
	}
}

func (s *MQTTSink) resyncLocked() {
	_ = s.publish(s.statusTopic(), []byte("online"), true)
	_ = s.publishDiscovery(s.configTopic("event", haID, "events"), map[string]any{
		"availability_topic": s.statusTopic(),
		"device":             s.hubDevice(),
		"event_types":        []string{string(vision.EventDetectorDown)},
		"name":               "Eventos",
		"origin":             s.origin(),
		"state_topic":        s.prefix() + "/event",
		"unique_id":          haID + "_events",
	})
	for _, info := range s.allCameras() {
		st := s.state(info.Cam)
		s.ensureZids(info.Cam, st)
		s.publishCameraDiscovery(info, st)
		_ = s.publishCamState(info.Cam, st, info, true)
	}
}

func (s *MQTTSink) publishCameraDiscovery(info CameraInfo, st *camState) {
	cam := info.Cam
	node := s.camNode(cam)
	device := s.camDevice(info)
	_ = s.publishDiscovery(s.configTopic("binary_sensor", node, "person"), map[string]any{
		"availability_topic": s.statusTopic(),
		"device":             device,
		"device_class":       "occupancy",
		"name":               "Persona",
		"origin":             s.origin(),
		"state_topic":        s.camTopic(cam, "person"),
		"unique_id":          fmt.Sprintf("%s_cam%d_person", haID, cam),
	})
	_ = s.publishDiscovery(s.configTopic("binary_sensor", node, "connected"), map[string]any{
		"availability_topic": s.statusTopic(),
		"device":             device,
		"device_class":       "connectivity",
		"entity_category":    "diagnostic",
		"name":               "Conectada",
		"origin":             s.origin(),
		"state_topic":        s.camTopic(cam, "connected"),
		"unique_id":          fmt.Sprintf("%s_cam%d_connected", haID, cam),
	})
	_ = s.publishDiscovery(s.configTopic("image", node, "snapshot"), map[string]any{
		"availability_topic": s.statusTopic(),
		"device":             device,
		"name":               "Captura",
		"origin":             s.origin(),
		"unique_id":          fmt.Sprintf("%s_cam%d_snapshot", haID, cam),
		"url_topic":          s.camTopic(cam, "snapshot_url"),
	})
	_ = s.publishDiscovery(s.configTopic("event", node, "events"), map[string]any{
		"availability_topic": s.statusTopic(),
		"device":             device,
		"event_types":        cameraEventTypes,
		"name":               "Eventos",
		"origin":             s.origin(),
		"state_topic":        s.camTopic(cam, "event"),
		"unique_id":          fmt.Sprintf("%s_cam%d_events", haID, cam),
	})

	current := map[string]vision.Zone{}
	if s.Zones != nil {
		for _, z := range s.Zones(cam) {
			if z.ID != "" {
				current[z.ID] = z
			}
		}
	}
	for orig, zid := range st.publishedZones {
		if _, ok := current[orig]; ok {
			continue
		}
		// Una zona borrada deja de contar para la presencia de la cámara.
		delete(st.occupied, orig)
		delete(st.lastZones, zid)
		cfgErr := s.publish(s.configTopic("binary_sensor", node, "zone_"+zid), nil, true)
		stateErr := s.publish(s.camTopic(cam, "zone/"+zid), nil, true)
		if cfgErr == nil && stateErr == nil {
			// Si falla, el próximo Resync vuelve a intentar el borrado.
			delete(st.publishedZones, orig)
		}
	}
	for orig, z := range current {
		zid := s.assignZid(st, orig)
		name := z.Name
		if name == "" {
			name = z.ID
		}
		_ = s.publishDiscovery(s.configTopic("binary_sensor", node, "zone_"+zid), map[string]any{
			"availability_topic": s.statusTopic(),
			"device":             device,
			"device_class":       "occupancy",
			"name":               name,
			"origin":             s.origin(),
			"state_topic":        s.camTopic(cam, "zone/"+zid),
			"unique_id":          fmt.Sprintf("%s_cam%d_zone_%s", haID, cam, zid),
		})
		st.publishedZones[orig] = zid
	}
}

func (s *MQTTSink) publishCamState(cam int, st *camState, info CameraInfo, force bool) error {
	var err error
	person := onOff(len(st.occupied) > 0 || st.tracking)
	if force || st.lastPerson != person {
		if pubErr := s.publish(s.camTopic(cam, "person"), []byte(person), true); pubErr != nil {
			err = firstErr(err, pubErr)
		} else {
			st.lastPerson = person
		}
	}
	err = firstErr(err, s.publishZoneStates(cam, st, force))

	conn := st.connected
	if conn == nil {
		conn = info.Online
	}
	if conn != nil && (force || !boolPtrEq(st.lastConnected, conn)) {
		if pubErr := s.publish(s.camTopic(cam, "connected"), []byte(onOff(*conn)), true); pubErr != nil {
			err = firstErr(err, pubErr)
		} else {
			st.lastConnected = boolPtr(*conn)
		}
	}
	if st.snapshotURL != "" && (force || st.lastSnapshot != st.snapshotURL) {
		if pubErr := s.publish(s.camTopic(cam, "snapshot_url"), []byte(st.snapshotURL), true); pubErr != nil {
			err = firstErr(err, pubErr)
		} else {
			st.lastSnapshot = st.snapshotURL
		}
	}
	return err
}

func (s *MQTTSink) publishZoneStates(cam int, st *camState, force bool) error {
	s.ensureZids(cam, st)
	desired := map[string]string{}
	if force && s.Zones != nil {
		for _, z := range s.Zones(cam) {
			if z.ID != "" {
				desired[s.assignZid(st, z.ID)] = "OFF"
			}
		}
	}
	for id := range st.occupied {
		if id == "" {
			continue
		}
		desired[s.assignZid(st, id)] = "ON"
	}
	var err error
	publish := func(zid, val string) {
		if pubErr := s.publish(s.camTopic(cam, "zone/"+zid), []byte(val), true); pubErr != nil {
			err = firstErr(err, pubErr)
			return
		}
		st.lastZones[zid] = val
	}
	if force {
		for zid, val := range desired {
			publish(zid, val)
		}
		return err
	}
	for zid, val := range desired {
		if st.lastZones[zid] != val {
			publish(zid, val)
		}
	}
	for zid, last := range st.lastZones {
		if _, ok := desired[zid]; !ok && last == "ON" {
			publish(zid, "OFF")
		}
	}
	return err
}

func (s *MQTTSink) publishEventJSON(ev vision.Event, cam int) error {
	payload := haEvent{
		EventType: string(ev.Kind),
		ID:        ev.ID,
		ZoneID:    ev.ZoneID,
		ZoneName:  s.zoneName(cam, ev.ZoneID),
		TrackID:   ev.TrackID,
		Message:   ev.Message,
	}
	if !ev.At.IsZero() {
		payload.At = ev.At.Format(time.RFC3339)
	}
	if ev.Snapshot != "" && s.SnapshotURL != nil {
		payload.SnapshotURL = s.SnapshotURL(ev.Snapshot)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("codificar evento: %w", err)
	}
	topic := s.prefix() + "/event"
	if cam != 0 {
		topic = s.camTopic(cam, "event")
	}
	return s.publish(topic, body, false)
}

func (s *MQTTSink) zoneName(cam int, id string) string {
	if id == "" || s.Zones == nil {
		return ""
	}
	for _, z := range s.Zones(cam) {
		if z.ID == id {
			if z.Name != "" {
				return z.Name
			}
			return z.ID
		}
	}
	return ""
}

func (s *MQTTSink) allCameras() []CameraInfo {
	byID := map[int]CameraInfo{}
	var order []int
	add := func(info CameraInfo) {
		prev, ok := byID[info.Cam]
		if !ok {
			order = append(order, info.Cam)
		} else {
			if info.Name == "" {
				info.Name = prev.Name
			}
			if info.Online == nil {
				info.Online = prev.Online
			}
		}
		byID[info.Cam] = info
	}
	if s.Cameras != nil {
		for _, c := range s.Cameras() {
			add(c)
		}
	}
	for cam := range s.cams {
		if _, ok := byID[cam]; !ok {
			add(CameraInfo{Cam: cam})
		}
	}
	out := make([]CameraInfo, 0, len(order))
	for _, id := range order {
		info := byID[id]
		if info.Name == "" {
			info.Name = cameraName(info)
		}
		out = append(out, info)
	}
	return out
}

func (s *MQTTSink) ensureZids(cam int, st *camState) {
	if s.Zones == nil {
		return
	}
	for _, z := range s.Zones(cam) {
		if z.ID != "" {
			s.assignZid(st, z.ID)
		}
	}
}

func (s *MQTTSink) assignZid(st *camState, id string) string {
	if id == "" {
		return ""
	}
	if zid, ok := st.zids[id]; ok {
		return zid
	}
	base := sanitizeZID(id)
	zid := base
	if owner, used := st.usedZids[zid]; used && owner != id {
		sum := sha256.Sum256([]byte(id))
		hexSum := hex.EncodeToString(sum[:])
		zid = base + "_" + hexSum[:6]
		if owner2, used2 := st.usedZids[zid]; used2 && owner2 != id {
			zid = base + "_" + hexSum[:12]
		}
	}
	st.zids[id] = zid
	st.usedZids[zid] = id
	return zid
}

func sanitizeZID(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (s *MQTTSink) publish(topic string, payload []byte, retain bool) error {
	if s.Pub == nil {
		return fmt.Errorf("publicar %s: sin Publisher", topic)
	}
	if payload == nil {
		payload = []byte{}
	}
	if err := s.Pub.Publish(topic, payload, retain); err != nil {
		return fmt.Errorf("publicar %s: %w", topic, err)
	}
	return nil
}

func (s *MQTTSink) publishDiscovery(topic string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("codificar discovery %s: %w", topic, err)
	}
	return s.publish(topic, body, true)
}

func onOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

func boolPtr(v bool) *bool { return &v }

func boolPtrEq(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func firstErr(first, err error) error {
	if first != nil {
		return first
	}
	return err
}
