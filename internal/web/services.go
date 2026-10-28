package web

import (
	"context"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

// ConfigService es la configuración del NVR (lo implementa *nvr.Client).
type ConfigService interface {
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
}

// RecordingService dice qué hay grabado (lo implementa *nvr.Client).
type RecordingService interface {
	RecordDays(ctx context.Context, year int, month time.Month) ([]int, error)
	RecordSegments(ctx context.Context, ch int, day time.Time) ([]nvr.Segment, error)
}

// EventHub reparte los eventos del NVR (lo implementa *camera.Hub).
type EventHub interface {
	Subscribe() (<-chan nvr.Event, func())
}
