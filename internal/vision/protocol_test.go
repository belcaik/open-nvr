package vision

import "testing"

func TestDecodeContractMessages(t *testing.T) {
	tests := []struct {
		name string
		json string
		want any
	}{
		{
			name: "detections",
			json: `{"type":"detections","cam":1,"seq":4711,"ts":"2026-11-03T15:30:01.123456Z","w":800,"h":448,"tracks":[{"id":7,"conf":0.83,"box":[0.412,0.130,0.501,0.774]}]}`,
			want: Frame{},
		},
		{
			name: "status",
			json: `{"type":"status","cam":1,"state":"ok","fps":9.7,"infer_ms_p50":18.2,"dropped":12}`,
			want: Status{},
		},
		{
			name: "unknown type",
			json: `{"type":"future","cam":0}`,
			want: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Decode([]byte(test.json))
			if err != nil {
				t.Fatal(err)
			}
			switch test.want.(type) {
			case Frame:
				frame, ok := got.(Frame)
				if !ok || frame.Cam != 1 || frame.Seq != 4711 || frame.W != 800 || frame.H != 448 || len(frame.Tracks) != 1 || frame.Tracks[0].ID != 7 {
					t.Fatalf("Frame = %#v", got)
				}
			case Status:
				status, ok := got.(Status)
				if !ok || status.State != "ok" || status.Dropped != 12 {
					t.Fatalf("Status = %#v", got)
				}
			default:
				if got != nil {
					t.Fatalf("mensaje desconocido = %#v, quiero nil", got)
				}
			}
		})
	}
}

func TestDecodeRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"inverted box", `{"type":"detections","cam":1,"tracks":[{"id":1,"box":[0.8,0.2,0.1,0.9]}]}`},
		{"camera zero in detections", `{"type":"detections","cam":0,"tracks":[]}`},
		{"camera zero in status", `{"type":"status","cam":0,"state":"ok"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode([]byte(test.json)); err == nil {
				t.Fatal("Decode aceptó un mensaje inválido")
			}
		})
	}
}

func TestDecodeAcceptsDisabledStatus(t *testing.T) {
	message, err := Decode([]byte(`{"type":"status","cam":3,"state":"disabled","fps":0,"infer_ms_p50":0,"dropped":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := message.(Status); !ok || status.State != "disabled" {
		t.Fatalf("mensaje = %#v", message)
	}
}
