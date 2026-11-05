package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/vision"
)

type pubMsg struct {
	topic   string
	payload string
	retain  bool
}

type fakePub struct {
	mu        sync.Mutex
	msgs      []pubMsg
	fail      error
	failTopic string
}

func (p *fakePub) Publish(topic string, payload []byte, retain bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, pubMsg{topic: topic, payload: string(payload), retain: retain})
	if p.fail != nil && (p.failTopic == "" || p.failTopic == topic) {
		return p.fail
	}
	return nil
}

func (p *fakePub) all() []pubMsg {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]pubMsg, len(p.msgs))
	copy(out, p.msgs)
	return out
}

func (p *fakePub) last(topic string) (pubMsg, bool) {
	msgs := p.all()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].topic == topic {
			return msgs[i], true
		}
	}
	return pubMsg{}, false
}

func (p *fakePub) count(topic string) int {
	n := 0
	for _, m := range p.all() {
		if m.topic == topic {
			n++
		}
	}
	return n
}

func (p *fakePub) reset() {
	p.mu.Lock()
	p.msgs = nil
	p.mu.Unlock()
}

func mustLast(t *testing.T, pub *fakePub, topic string) pubMsg {
	t.Helper()
	msg, ok := pub.last(topic)
	if !ok {
		t.Fatalf("no se publicó %s; mensajes = %v", topic, pub.all())
	}
	return msg
}

func jsonEqual(t *testing.T, got []byte, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("obtenido no es JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("esperado no es JSON: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON distinto\nobtenido: %s\nquerido:  %s", got, want)
	}
}

func testSink(pub Publisher, zones []vision.Zone) *MQTTSink {
	online := true
	z := zones
	return &MQTTSink{
		Pub: pub,
		Cameras: func() []CameraInfo {
			return []CameraInfo{{Cam: 1, Name: "Entrada", Online: &online}}
		},
		Zones: func(cam int) []vision.Zone {
			if cam != 1 {
				return nil
			}
			return z
		},
		SnapshotURL: func(rel string) string {
			if rel == "" {
				return ""
			}
			return "http://nvr/vision/" + rel
		},
	}
}

func TestMQTTSinkMapsEventToTopics(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta", Name: "Puerta principal"}})
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ev := vision.Event{
		ID: "e1", Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta",
		TrackID: 3, At: at, Message: "entra", Snapshot: "snapshots/e1.jpg",
	}
	if err := sink.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	person := mustLast(t, pub, "opennvr/cam1/person")
	if person.payload != "ON" || !person.retain {
		t.Fatalf("person = %+v", person)
	}
	zone := mustLast(t, pub, "opennvr/cam1/zone/puerta")
	if zone.payload != "ON" || !zone.retain {
		t.Fatalf("zona = %+v", zone)
	}
	snap := mustLast(t, pub, "opennvr/cam1/snapshot_url")
	if snap.payload != "http://nvr/vision/snapshots/e1.jpg" || !snap.retain {
		t.Fatalf("captura = %+v", snap)
	}
	evt := mustLast(t, pub, "opennvr/cam1/event")
	if evt.retain {
		t.Fatal("el evento de cámara no debe retenerse")
	}
	jsonEqual(t, []byte(evt.payload), []byte(`{"event_type":"zone_enter","id":"e1","zone_id":"Puerta","zone_name":"Puerta principal","track_id":3,"at":"2026-11-04T12:00:00Z","message":"entra","snapshot_url":"http://nvr/vision/snapshots/e1.jpg"}`))
	if _, ok := pub.last("opennvr/cam1/connected"); ok {
		t.Fatal("connected no se publica si se desconoce")
	}
	if _, ok := pub.last("opennvr/status"); ok {
		t.Fatal("Send no publica el estado del hub")
	}
}

func TestMQTTSinkAggregatesPersonFromZonesAndTracking(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta", Name: "Puerta"}})
	ctx := context.Background()

	if err := sink.Send(ctx, vision.Event{Kind: vision.EventTrackingStart, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "ON" {
		t.Fatal("tracking_start debía encender persona")
	}
	pub.reset()
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventTrackingStop, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "OFF" {
		t.Fatal("tracking_stop sin zonas debía apagar persona")
	}

	if err := sink.Send(ctx, vision.Event{Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventTrackingStart, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	pub.reset()
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventTrackingStop, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	if msg, ok := pub.last("opennvr/cam1/person"); ok && msg.payload != "ON" {
		t.Fatalf("persona con zona ocupada = %q", msg.payload)
	}
	if pub.count("opennvr/cam1/person") != 0 {
		t.Fatal("no debía republicar persona si sigue ON")
	}

	if err := sink.Send(ctx, vision.Event{Kind: vision.EventZoneLeave, Cam: 1, ZoneID: "Puerta"}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "OFF" {
		t.Fatal("salir de la última zona debía apagar persona")
	}

	pub.reset()
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventZoneEnter, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "ON" {
		t.Fatal("zone_enter sin ZoneID cuenta para persona")
	}
	if _, ok := pub.last("opennvr/cam1/zone/"); ok {
		t.Fatal("ZoneID vacío no tiene sensor de zona")
	}
}

func TestMQTTSinkCameraOfflineResetsOccupancy(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta", Name: "Puerta"}})
	ctx := context.Background()
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventTrackingStart, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	pub.reset()
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventCameraOffline, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "OFF" {
		t.Fatal("camera_offline debía apagar persona")
	}
	if mustLast(t, pub, "opennvr/cam1/zone/puerta").payload != "OFF" {
		t.Fatal("camera_offline debía vaciar zonas")
	}
	conn := mustLast(t, pub, "opennvr/cam1/connected")
	if conn.payload != "OFF" || !conn.retain {
		t.Fatalf("connected = %+v", conn)
	}
	pub.reset()
	if err := sink.Send(ctx, vision.Event{Kind: vision.EventCameraOnline, Cam: 1}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/connected").payload != "ON" {
		t.Fatal("camera_online debía publicar connected ON")
	}
	if msg, ok := pub.last("opennvr/cam1/person"); ok && msg.payload != "OFF" {
		t.Fatalf("persona tras online = %q", msg.payload)
	}
}

func TestMQTTSinkDiscoveryJSONGolden(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta", Name: "Puerta principal"}})
	sink.Resync()

	status := mustLast(t, pub, "opennvr/status")
	if status.payload != "online" || !status.retain {
		t.Fatalf("status = %+v", status)
	}

	cases := []struct {
		topic string
		file  string
	}{
		{"homeassistant/binary_sensor/opennvr_cam1/person/config", "ha_binary_sensor_person.json"},
		{"homeassistant/image/opennvr_cam1/snapshot/config", "ha_image_snapshot.json"},
		{"homeassistant/event/opennvr_cam1/events/config", "ha_event_camera.json"},
	}
	for _, c := range cases {
		msg := mustLast(t, pub, c.topic)
		if !msg.retain {
			t.Fatalf("%s no está retenido", c.topic)
		}
		want, err := os.ReadFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(err)
		}
		jsonEqual(t, []byte(msg.payload), want)
	}

	conn := mustLast(t, pub, "homeassistant/binary_sensor/opennvr_cam1/connected/config")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(conn.payload), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["device_class"] != "connectivity" || parsed["entity_category"] != "diagnostic" {
		t.Fatalf("connected discovery = %s", conn.payload)
	}
	if _, ok := pub.last("homeassistant/event/opennvr/events/config"); !ok {
		t.Fatal("falta el discovery de eventos de sistema")
	}
	if mustLast(t, pub, "opennvr/cam1/connected").payload != "ON" {
		t.Fatal("Resync publica connected si CameraInfo.Online lo conoce")
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "OFF" {
		t.Fatal("Resync publica persona OFF sin ocupación")
	}
}

func TestMQTTSinkDeletesRemovedZones(t *testing.T) {
	pub := &fakePub{}
	zones := []vision.Zone{{ID: "Puerta", Name: "Puerta"}}
	sink := testSink(pub, nil)
	sink.Zones = func(cam int) []vision.Zone {
		if cam != 1 {
			return nil
		}
		return zones
	}
	sink.Resync()
	if _, ok := pub.last("homeassistant/binary_sensor/opennvr_cam1/zone_puerta/config"); !ok {
		t.Fatal("faltaba el discovery de zona")
	}
	zones = nil
	pub.reset()
	sink.Resync()
	cfg := mustLast(t, pub, "homeassistant/binary_sensor/opennvr_cam1/zone_puerta/config")
	if cfg.payload != "" || !cfg.retain {
		t.Fatalf("borrado de config = %+v", cfg)
	}
	st := mustLast(t, pub, "opennvr/cam1/zone/puerta")
	if st.payload != "" || !st.retain {
		t.Fatalf("borrado de estado = %+v", st)
	}
}

func TestMQTTSinkHAStatusOnlineResyncs(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, nil)
	sink.Resync()
	n := len(pub.all())
	sink.HAStatus([]byte("offline"))
	if len(pub.all()) != n {
		t.Fatal("HAStatus offline no debe republicar")
	}
	sink.HAStatus([]byte("online"))
	if len(pub.all()) <= n {
		t.Fatal("HAStatus online debía llamar a Resync")
	}
}

func TestMQTTSinkSanitizesAndDisambiguatesZoneIDs(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{
		{ID: "A B", Name: "uno"},
		{ID: "A/B", Name: "dos"},
	})
	sink.Resync()
	if _, ok := pub.last("opennvr/cam1/zone/a_b"); !ok {
		t.Fatalf("faltaba a_b; %v", pub.all())
	}
	sum := sha256.Sum256([]byte("A/B"))
	zid := "a_b_" + hex.EncodeToString(sum[:])[:6]
	if _, ok := pub.last("opennvr/cam1/zone/" + zid); !ok {
		t.Fatalf("faltaba %s; %v", zid, pub.all())
	}
	if _, ok := pub.last("homeassistant/binary_sensor/opennvr_cam1/zone_" + zid + "/config"); !ok {
		t.Fatal("faltaba discovery de la zona en colisión")
	}
	pub.reset()
	if err := sink.Send(context.Background(), vision.Event{Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "A/B"}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/zone/"+zid).payload != "ON" {
		t.Fatal("el evento debía usar el zid desambiguado")
	}
}

func TestMQTTSinkSendPublishesOnlyChanges(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta"}})
	ctx := context.Background()
	ev := vision.Event{ID: "a", Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta"}
	if err := sink.Send(ctx, ev); err != nil {
		t.Fatal(err)
	}
	persons := pub.count("opennvr/cam1/person")
	zones := pub.count("opennvr/cam1/zone/puerta")
	ev.ID = "b"
	if err := sink.Send(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if pub.count("opennvr/cam1/person") != persons {
		t.Fatal("no debía republicar persona sin cambio")
	}
	if pub.count("opennvr/cam1/zone/puerta") != zones {
		t.Fatal("no debía republicar zona sin cambio")
	}
	if pub.count("opennvr/cam1/event") != 2 {
		t.Fatal("cada Send publica el JSON del evento")
	}
}

func TestMQTTSinkDetectorDownGoesToSystemTopic(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, nil)
	ev := vision.Event{ID: "down", Kind: vision.EventDetectorDown, At: time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC)}
	if err := sink.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	msg := mustLast(t, pub, "opennvr/event")
	if msg.retain {
		t.Fatal("el evento de sistema no debe retenerse")
	}
	jsonEqual(t, []byte(msg.payload), []byte(`{"event_type":"detector_down","id":"down","at":"2026-11-04T13:00:00Z"}`))
	if _, ok := pub.last("opennvr/cam0/event"); ok {
		t.Fatal("detector_down no es un evento de cámara")
	}
}

func TestMQTTSinkUnknownCameraGetsDefaultName(t *testing.T) {
	pub := &fakePub{}
	sink := &MQTTSink{Pub: pub}
	if err := sink.Send(context.Background(), vision.Event{Kind: vision.EventZoneEnter, Cam: 9, ZoneID: "x"}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam9/person").payload != "ON" {
		t.Fatal("debía gestionar la cámara ausente de Cameras()")
	}
	sink.Resync()
	cfg := mustLast(t, pub, "homeassistant/binary_sensor/opennvr_cam9/person/config")
	if !strings.Contains(cfg.payload, `"Cámara 9"`) {
		t.Fatalf("nombre del dispositivo = %s", cfg.payload)
	}
}

func TestMQTTSinkPublishErrorKeepsStateAndContinues(t *testing.T) {
	pub := &fakePub{fail: errors.New("broker caído"), failTopic: "opennvr/cam1/person"}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta"}})
	err := sink.Send(context.Background(), vision.Event{Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta"})
	if err == nil || !strings.Contains(err.Error(), "opennvr/cam1/person") || !strings.Contains(err.Error(), "broker caído") {
		t.Fatalf("error = %v", err)
	}
	if _, ok := pub.last("opennvr/cam1/zone/puerta"); !ok {
		t.Fatal("debía seguir publicando el resto")
	}
	if _, ok := pub.last("opennvr/cam1/event"); !ok {
		t.Fatal("debía publicar el evento pese al error")
	}
	pub.fail = nil
	pub.failTopic = ""
	pub.reset()
	if err := sink.Send(context.Background(), vision.Event{Kind: vision.EventZoneLeave, Cam: 1, ZoneID: "Puerta"}); err != nil {
		t.Fatal(err)
	}
	if mustLast(t, pub, "opennvr/cam1/person").payload != "OFF" {
		t.Fatal("el estado interno debía haberse actualizado tras el error")
	}
}

func TestMQTTSinkDefaultsPrefixAndDiscovery(t *testing.T) {
	pub := &fakePub{}
	sink := &MQTTSink{
		Pub: pub,
		Cameras: func() []CameraInfo {
			return []CameraInfo{{Cam: 2, Name: "Patio"}}
		},
	}
	sink.Resync()
	if _, ok := pub.last("opennvr/status"); !ok {
		t.Fatal("prefijo por defecto opennvr")
	}
	if _, ok := pub.last("homeassistant/binary_sensor/opennvr_cam2/person/config"); !ok {
		t.Fatal("discovery por defecto homeassistant")
	}
}

func TestMQTTSinkConcurrentSendResyncAndHAStatus(t *testing.T) {
	pub := &fakePub{}
	sink := testSink(pub, []vision.Zone{{ID: "Puerta"}})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = sink.Send(ctx, vision.Event{Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta"})
		}()
		go func() {
			defer wg.Done()
			sink.Resync()
		}()
		go func() {
			defer wg.Done()
			sink.HAStatus([]byte("online"))
		}()
	}
	wg.Wait()
}
