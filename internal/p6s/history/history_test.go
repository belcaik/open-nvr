package history

import (
	"strings"
	"testing"
)

func validCreateRequest() HistoryStreamCreateRequest {
	return HistoryStreamCreateRequest{
		Channel: 3, DiskGroup: "2", EnableEndTime: 1, StreamType: 1,
		TransportType: "0", StreamMediaType: "1", TaskType: TaskPlayback, Version: "2",
		BeginTime: DateTime{Year: 2024, Month: 2, Day: 29, Hour: 23, Minute: 59, Second: 58},
		EndTime:   DateTime{Year: 2024, Month: 3, Day: 1, Hour: 0, Minute: 0, Second: 0},
	}
}

func TestRequestGolden(t *testing.T) {
	tests := []struct {
		name  string
		id    uint16
		build func() (Message, error)
		want  string
	}{
		{
			name: "HistoryStreamCreateMultiType", id: 185,
			build: func() (Message, error) {
				return NewHistoryStreamCreateMultiType(HistoryStreamCreateMultiTypeRequest{
					DiskGroup: 1, Channel: 0, EnableEndTime: 0, StreamType: 63,
					TransportType: 1, StreamMediaType: 0, TaskType: 1001, Version: -1,
					BeginTime: DateTime{Year: 2026, Month: 10, Day: 28, Hour: 0, Minute: 0, Second: 0},
					EndTime:   DateTime{Year: 2026, Month: 10, Day: 28, Hour: 23, Minute: 59, Second: 59},
				})
			},
			want: `<Command ID="185"><HistoryStreamCreateMultiType Version="-1"><HistoryStreamPara DiskGroup="1" Channel="0" EnableEndTime="0" StreamType="63" TransportType="1" StreamMediaType="0" TaskType="1001"><BeginTime Year="2026" Month="10" Day="28" Hour="0" Minute="0" Second="0"></BeginTime><EndTime Year="2026" Month="10" Day="28" Hour="23" Minute="59" Second="59"></EndTime></HistoryStreamPara></HistoryStreamCreateMultiType></Command>`,
		},
		{
			name: "HistoryStreamCreate", id: 124,
			build: func() (Message, error) { return NewHistoryStreamCreate(validCreateRequest()) },
			want:  `<Command ID="124"><HistoryStreamCreate Channel="3" DiskGroup="2" EnableEndTime="1" StreamType="1" TransportType="0" StreamMediaType="1" TaskType="1" Version="2"><HistoryStreamPara><BeginTime Year="2024" Month="2" Day="29" Hour="23" Minute="59" Second="58"></BeginTime><EndTime Year="2024" Month="3" Day="1" Hour="0" Minute="0" Second="0"></EndTime></HistoryStreamPara></HistoryStreamCreate></Command>`,
		},
		{
			name: "HistoryStreamDestroy", id: 126,
			build: func() (Message, error) {
				return NewHistoryStreamDestroy(HistoryStreamDestroyRequest{MediaFlag: "2003281556", Version: "2"})
			},
			want: `<Command ID="126"><HistoryStreamDestroy MediaFlag="2003281556" Version="2"></HistoryStreamDestroy></Command>`,
		},
		{
			name: "HistoryStreamPos", id: 128,
			build: func() (Message, error) {
				return NewHistoryStreamPos(HistoryStreamPosRequest{
					MediaFlag: "77",
					PosTime:   DateTime{Year: 2024, Month: 3, Day: 1, Hour: 0, Minute: 1, Second: 2},
				})
			},
			want: `<Command ID="128"><HistoryStreamPos Operation="2" MediaFlag="77" StreamFlag="1"><PosTime Year="2024" Month="3" Day="1" Hour="0" Minute="1" Second="2"></PosTime></HistoryStreamPos></Command>`,
		},
		{
			name: "HistoryStreamCtlSpeed", id: 130,
			build: func() (Message, error) {
				return NewHistoryStreamCtl(HistoryStreamCtlRequest{Operation: ControlSpeed, MediaFlag: "77", Speed: "4"})
			},
			want: `<Command ID="130"><HistoryStreamCtl Operation="3" MediaFlag="77" Speed="4"></HistoryStreamCtl></Command>`,
		},
		{
			name: "FileDownloadCreate", id: 56,
			build: func() (Message, error) {
				return NewFileDownloadCreate(FileDownloadCreateRequest{FileType: "1", FileName: "cam3&amp;.dav"})
			},
			want: `<Command ID="56"><FileDownloadCreate FileType="1" FileName="cam3&amp;amp;.dav"></FileDownloadCreate></Command>`,
		},
		{
			name: "FileDownloadRelease", id: 59,
			build: func() (Message, error) {
				return NewFileDownloadRelease(FileDownloadReleaseRequest{MediaFlag: "88"})
			},
			want: `<Command ID="59"><FileDownloadRelease MediaFlag="88"></FileDownloadRelease></Command>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, err := test.build()
			if err != nil {
				t.Fatal(err)
			}
			if message.ID != test.id {
				t.Fatalf("ID = %d, want %d", message.ID, test.id)
			}
			if got := string(message.Payload); got != test.want {
				t.Fatalf("payload:\n got %s\nwant %s", got, test.want)
			}
		})
	}
}

func TestHistoryStreamCreateBoundaries(t *testing.T) {
	for _, channel := range []int{1, 7} {
		for _, stream := range []int{1, 2} {
			request := validCreateRequest()
			request.Channel = channel
			request.StreamType = stream
			request.TaskType = TaskDownload
			request.EnableEndTime = 0
			request.BeginTime, request.EndTime = request.EndTime, request.BeginTime
			if _, err := NewHistoryStreamCreate(request); err != nil {
				t.Fatalf("Channel=%d StreamType=%d: %v", channel, stream, err)
			}
		}
	}
}

func TestHistoryStreamCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HistoryStreamCreateRequest)
	}{
		{"ChannelZero", func(r *HistoryStreamCreateRequest) { r.Channel = 0 }},
		{"ChannelEight", func(r *HistoryStreamCreateRequest) { r.Channel = 8 }},
		{"StreamZero", func(r *HistoryStreamCreateRequest) { r.StreamType = 0 }},
		{"StreamThree", func(r *HistoryStreamCreateRequest) { r.StreamType = 3 }},
		{"EnableEndTime", func(r *HistoryStreamCreateRequest) { r.EnableEndTime = 2 }},
		{"TaskType", func(r *HistoryStreamCreateRequest) { r.TaskType = 2 }},
		{"DiskGroupRequired", func(r *HistoryStreamCreateRequest) { r.DiskGroup = "" }},
		{"TransportTypeRequired", func(r *HistoryStreamCreateRequest) { r.TransportType = "" }},
		{"StreamMediaTypeRequired", func(r *HistoryStreamCreateRequest) { r.StreamMediaType = "" }},
		{"VersionRequired", func(r *HistoryStreamCreateRequest) { r.Version = "" }},
		{"Month", func(r *HistoryStreamCreateRequest) { r.BeginTime.Month = 13 }},
		{"DayZero", func(r *HistoryStreamCreateRequest) { r.BeginTime.Day = 0 }},
		{"NonLeapDay", func(r *HistoryStreamCreateRequest) {
			r.BeginTime = DateTime{Year: 2023, Month: 2, Day: 29}
		}},
		{"Hour", func(r *HistoryStreamCreateRequest) { r.BeginTime.Hour = 24 }},
		{"Minute", func(r *HistoryStreamCreateRequest) { r.BeginTime.Minute = 60 }},
		{"Second", func(r *HistoryStreamCreateRequest) { r.BeginTime.Second = 60 }},
		{"InvalidEndTime", func(r *HistoryStreamCreateRequest) { r.EndTime.Day = 31; r.EndTime.Month = 4 }},
		{"BeginAfterEnd", func(r *HistoryStreamCreateRequest) {
			r.BeginTime, r.EndTime = r.EndTime, r.BeginTime
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCreateRequest()
			test.mutate(&request)
			if _, err := NewHistoryStreamCreate(request); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOtherRequestsRejectInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		build func() error
	}{
		{"DestroyMediaFlag", func() error {
			_, err := NewHistoryStreamDestroy(HistoryStreamDestroyRequest{})
			return err
		}},
		{"DestroyVersion", func() error {
			_, err := NewHistoryStreamDestroy(HistoryStreamDestroyRequest{MediaFlag: "1"})
			return err
		}},
		{"PosMediaFlag", func() error {
			_, err := NewHistoryStreamPos(HistoryStreamPosRequest{
				PosTime: DateTime{Year: 2024, Month: 3, Day: 1},
			})
			return err
		}},
		{"PosTimeRequired", func() error {
			_, err := NewHistoryStreamPos(HistoryStreamPosRequest{MediaFlag: "1"})
			return err
		}},
		{"PosTimeInvalid", func() error {
			_, err := NewHistoryStreamPos(HistoryStreamPosRequest{
				MediaFlag: "1",
				PosTime:   DateTime{Year: 2023, Month: 2, Day: 29},
			})
			return err
		}},
		{"CtlOperationZero", func() error {
			_, err := NewHistoryStreamCtl(HistoryStreamCtlRequest{MediaFlag: "1"})
			return err
		}},
		{"CtlOperationFour", func() error {
			_, err := NewHistoryStreamCtl(HistoryStreamCtlRequest{Operation: 4, MediaFlag: "1"})
			return err
		}},
		{"CtlMediaFlag", func() error {
			_, err := NewHistoryStreamCtl(HistoryStreamCtlRequest{Operation: ControlPauseStop})
			return err
		}},
		{"CtlSpeed", func() error {
			_, err := NewHistoryStreamCtl(HistoryStreamCtlRequest{Operation: ControlSpeed, MediaFlag: "1"})
			return err
		}},
		{"DownloadFileType", func() error {
			_, err := NewFileDownloadCreate(FileDownloadCreateRequest{FileName: "a.dav"})
			return err
		}},
		{"DownloadFileName", func() error {
			_, err := NewFileDownloadCreate(FileDownloadCreateRequest{FileType: "1"})
			return err
		}},
		{"ReleaseMediaFlag", func() error {
			_, err := NewFileDownloadRelease(FileDownloadReleaseRequest{})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.build(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestHistoryStreamCtlStartAndPause(t *testing.T) {
	for _, operation := range []ControlOperation{ControlStartResume, ControlPauseStop} {
		message, err := NewHistoryStreamCtl(HistoryStreamCtlRequest{Operation: operation, MediaFlag: "9"})
		if err != nil {
			t.Fatal(err)
		}
		payload := string(message.Payload)
		if !strings.Contains(payload, `Operation="`+string(rune('0'+operation))+`"`) {
			t.Fatalf("Operation %d missing: %s", operation, payload)
		}
		if !strings.Contains(payload, `Speed=""`) {
			t.Fatalf("Speed attribute missing: %s", payload)
		}
	}
}
