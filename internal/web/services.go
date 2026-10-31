package web

import (
	"context"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/playback"
)

// ConfigService es la configuración del NVR (lo implementa *nvr.Client).
type ConfigService interface {
	OSD(ctx context.Context, ch int) (nvr.OSD, error)
	SetOSD(ctx context.Context, ch int, v nvr.OSD) error
	WhiteBalance(ctx context.Context, ch int) (nvr.WhiteBalance, error)
	SetWhiteBalance(ctx context.Context, ch int, v nvr.WhiteBalance) error
	Exposure(ctx context.Context, ch int) (nvr.Exposure, error)
	SetExposure(ctx context.Context, ch int, v nvr.Exposure) error
	Scene(ctx context.Context, ch int) (nvr.Scene, error)
	SetScene(ctx context.Context, ch int, v nvr.Scene) error
	ImageColor(ctx context.Context, ch int) (nvr.ImageColor, error)
	SetImageColor(ctx context.Context, ch int, v nvr.ImageColor) error
	ImageBasic(ctx context.Context, ch int) (nvr.ImageBasic, error)
	SetImageBasic(ctx context.Context, ch int, v nvr.ImageBasic) error
	IrCut(ctx context.Context, ch int) (nvr.IrCut, error)
	SetIrCut(ctx context.Context, ch int, v nvr.IrCut) error
	Motion(ctx context.Context, ch int) (nvr.Motion, error)
	SetMotion(ctx context.Context, ch int, v nvr.Motion) error
	RecordSchedule(ctx context.Context, ch int) (nvr.RecordSchedule, error)
	SetRecordSchedule(ctx context.Context, ch int, v nvr.RecordSchedule) error
	Stream(ctx context.Context, ch, stream int) (nvr.StreamInfo, error)
	RecordSettings(ctx context.Context) (nvr.RecordSettings, error)
	SetRecordSettings(ctx context.Context, v nvr.RecordSettings) error
	Clock(ctx context.Context) (nvr.Clock, error)
	SyncNTP(ctx context.Context) error
	Disks(ctx context.Context) ([]nvr.Disk, error)
	Network(ctx context.Context) (nvr.NetworkInfo, error)
	P2P(ctx context.Context) (nvr.P2P, error)
	SetP2P(ctx context.Context, enabled bool) error
	AudioStream(ctx context.Context) (nvr.AudioStream, error)
	SetAudioStream(ctx context.Context, v nvr.AudioStream) error
	Logs(ctx context.Context, majorType string, start, stop time.Time) ([]nvr.LogEntry, error)
}

// RecordingService dice qué hay grabado (lo implementa *nvr.Client).
type RecordingService interface {
	RecordDays(ctx context.Context, year int, month time.Month) ([]int, error)
	RecordSegments(ctx context.Context, ch int, day time.Time) ([]nvr.Segment, error)
}

// PlaybackService controla sesiones históricas opacas; la web nunca maneja
// conexiones P6S ni URLs de fuente.
type PlaybackService interface {
	Open(context.Context, playback.Request) (playback.View, error)
	Status(id string) (playback.View, bool)
	Pause(context.Context, string) error
	Resume(context.Context, string) error
	Seek(context.Context, string, time.Time) error
	ClosePlayback(string) error
}

// EventHub reparte los eventos del NVR (lo implementa *camera.Hub).
type EventHub interface {
	Subscribe() (<-chan nvr.Event, func())
}
