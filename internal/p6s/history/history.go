package history

import (
	"encoding/xml"
	"fmt"
	"time"
)

// TaskType selecciona reproducción o descarga en HistoryStreamCreate.
type TaskType int

const (
	TaskDownload TaskType = 0
	TaskPlayback TaskType = 1
)

// ControlOperation selecciona la operación de HistoryStreamCtl.
type ControlOperation int

const (
	ControlStartResume ControlOperation = 1
	ControlPauseStop   ControlOperation = 2
	ControlSpeed       ControlOperation = 3
)

// DateTime representa los seis atributos temporales del protocolo.
type DateTime struct {
	Year   int `xml:"Year,attr"`
	Month  int `xml:"Month,attr"`
	Day    int `xml:"Day,attr"`
	Hour   int `xml:"Hour,attr"`
	Minute int `xml:"Minute,attr"`
	Second int `xml:"Second,attr"`
}

func (d DateTime) time() (time.Time, error) {
	value := time.Date(d.Year, time.Month(d.Month), d.Day, d.Hour, d.Minute, d.Second, 0, time.UTC)
	if value.Year() != d.Year || int(value.Month()) != d.Month || value.Day() != d.Day ||
		value.Hour() != d.Hour || value.Minute() != d.Minute || value.Second() != d.Second {
		return time.Time{}, fmt.Errorf("history: fecha inválida %04d-%02d-%02d %02d:%02d:%02d", d.Year, d.Month, d.Day, d.Hour, d.Minute, d.Second)
	}
	return value, nil
}

// HistoryStreamCreateRequest contiene todos los atributos necesarios para ID 124.
type HistoryStreamCreateRequest struct {
	Channel         int
	DiskGroup       string
	EnableEndTime   int
	StreamType      int
	TransportType   string
	StreamMediaType string
	TaskType        TaskType
	Version         string
	BeginTime       DateTime
	EndTime         DateTime
}

type historyStreamCreate struct {
	XMLName         xml.Name `xml:"HistoryStreamCreate"`
	Channel         int      `xml:"Channel,attr"`
	DiskGroup       string   `xml:"DiskGroup,attr"`
	EnableEndTime   int      `xml:"EnableEndTime,attr"`
	StreamType      int      `xml:"StreamType,attr"`
	TransportType   string   `xml:"TransportType,attr"`
	StreamMediaType string   `xml:"StreamMediaType,attr"`
	TaskType        TaskType `xml:"TaskType,attr"`
	Version         string   `xml:"Version,attr"`
	Parameters      struct {
		BeginTime DateTime `xml:"BeginTime"`
		EndTime   DateTime `xml:"EndTime"`
	} `xml:"HistoryStreamPara"`
}

// HistoryStreamCreateMultiTypeRequest contiene los valores observados para
// HistoryStreamCreateMultiType (ID 185).
type HistoryStreamCreateMultiTypeRequest struct {
	DiskGroup       int
	Channel         int
	EnableEndTime   int
	StreamType      int
	TransportType   int
	StreamMediaType int
	TaskType        int
	Version         int
	BeginTime       DateTime
	EndTime         DateTime
}

// NewHistoryStreamCreateMultiType valida y serializa
// HistoryStreamCreateMultiType (ID 185).
func NewHistoryStreamCreateMultiType(request HistoryStreamCreateMultiTypeRequest) (Message, error) {
	begin, err := request.BeginTime.time()
	if err != nil {
		return Message{}, fmt.Errorf("BeginTime: %w", err)
	}
	end, err := request.EndTime.time()
	if err != nil {
		return Message{}, fmt.Errorf("EndTime: %w", err)
	}
	if request.EnableEndTime != 0 && request.EnableEndTime != 1 {
		return Message{}, fmt.Errorf("history: EnableEndTime debe ser 0 o 1")
	}
	if request.EnableEndTime == 1 && begin.After(end) {
		return Message{}, fmt.Errorf("history: BeginTime no puede ser posterior a EndTime")
	}
	body := struct {
		XMLName    xml.Name `xml:"HistoryStreamCreateMultiType"`
		Version    int      `xml:"Version,attr"`
		Parameters struct {
			DiskGroup       int      `xml:"DiskGroup,attr"`
			Channel         int      `xml:"Channel,attr"`
			EnableEndTime   int      `xml:"EnableEndTime,attr"`
			StreamType      int      `xml:"StreamType,attr"`
			TransportType   int      `xml:"TransportType,attr"`
			StreamMediaType int      `xml:"StreamMediaType,attr"`
			TaskType        int      `xml:"TaskType,attr"`
			BeginTime       DateTime `xml:"BeginTime"`
			EndTime         DateTime `xml:"EndTime"`
		} `xml:"HistoryStreamPara"`
	}{Version: request.Version}
	body.Parameters.DiskGroup = request.DiskGroup
	body.Parameters.Channel = request.Channel
	body.Parameters.EnableEndTime = request.EnableEndTime
	body.Parameters.StreamType = request.StreamType
	body.Parameters.TransportType = request.TransportType
	body.Parameters.StreamMediaType = request.StreamMediaType
	body.Parameters.TaskType = request.TaskType
	body.Parameters.BeginTime = request.BeginTime
	body.Parameters.EndTime = request.EndTime
	return marshalCommand(IDHistoryStreamCreateMultiType, body)
}

// NewHistoryStreamCreate valida y serializa HistoryStreamCreate (ID 124).
func NewHistoryStreamCreate(request HistoryStreamCreateRequest) (Message, error) {
	if request.Channel < 1 || request.Channel > 7 {
		return Message{}, fmt.Errorf("history: Channel debe estar entre 1 y 7")
	}
	if request.StreamType != 1 && request.StreamType != 2 {
		return Message{}, fmt.Errorf("history: StreamType debe ser 1 o 2")
	}
	if request.EnableEndTime != 0 && request.EnableEndTime != 1 {
		return Message{}, fmt.Errorf("history: EnableEndTime debe ser 0 o 1")
	}
	if request.TaskType != TaskDownload && request.TaskType != TaskPlayback {
		return Message{}, fmt.Errorf("history: TaskType debe ser 0 o 1")
	}
	for field, value := range map[string]string{
		"DiskGroup": request.DiskGroup, "TransportType": request.TransportType,
		"StreamMediaType": request.StreamMediaType, "Version": request.Version,
	} {
		if err := require(field, value); err != nil {
			return Message{}, err
		}
	}
	begin, err := request.BeginTime.time()
	if err != nil {
		return Message{}, fmt.Errorf("BeginTime: %w", err)
	}
	end, err := request.EndTime.time()
	if err != nil {
		return Message{}, fmt.Errorf("EndTime: %w", err)
	}
	if request.EnableEndTime == 1 && begin.After(end) {
		return Message{}, fmt.Errorf("history: BeginTime no puede ser posterior a EndTime")
	}

	body := historyStreamCreate{
		Channel: request.Channel, DiskGroup: request.DiskGroup,
		EnableEndTime: request.EnableEndTime, StreamType: request.StreamType,
		TransportType: request.TransportType, StreamMediaType: request.StreamMediaType,
		TaskType: request.TaskType, Version: request.Version,
	}
	body.Parameters.BeginTime = request.BeginTime
	body.Parameters.EndTime = request.EndTime
	return marshalCommand(IDHistoryStreamCreate, body)
}

// HistoryStreamDestroyRequest contiene los atributos necesarios para ID 126.
type HistoryStreamDestroyRequest struct {
	MediaFlag string
	Version   string
}

// NewHistoryStreamDestroy valida y serializa HistoryStreamDestroy (ID 126).
func NewHistoryStreamDestroy(request HistoryStreamDestroyRequest) (Message, error) {
	if err := require("MediaFlag", request.MediaFlag); err != nil {
		return Message{}, err
	}
	if err := require("Version", request.Version); err != nil {
		return Message{}, err
	}
	body := struct {
		XMLName   xml.Name `xml:"HistoryStreamDestroy"`
		MediaFlag string   `xml:"MediaFlag,attr"`
		Version   string   `xml:"Version,attr"`
	}{MediaFlag: request.MediaFlag, Version: request.Version}
	return marshalCommand(IDHistoryStreamDestroy, body)
}

// HistoryStreamPosRequest contiene los campos variables para ID 128.
type HistoryStreamPosRequest struct {
	MediaFlag string
	PosTime   DateTime
}

// NewHistoryStreamPos serializa el seek capturado con Operation=2 y StreamFlag=1.
func NewHistoryStreamPos(request HistoryStreamPosRequest) (Message, error) {
	if err := require("MediaFlag", request.MediaFlag); err != nil {
		return Message{}, err
	}
	if _, err := request.PosTime.time(); err != nil {
		return Message{}, fmt.Errorf("PosTime: %w", err)
	}
	body := struct {
		XMLName    xml.Name `xml:"HistoryStreamPos"`
		Operation  int      `xml:"Operation,attr"`
		MediaFlag  string   `xml:"MediaFlag,attr"`
		StreamFlag int      `xml:"StreamFlag,attr"`
		PosTime    DateTime `xml:"PosTime"`
	}{Operation: 2, MediaFlag: request.MediaFlag, StreamFlag: 1, PosTime: request.PosTime}
	return marshalCommand(IDHistoryStreamPos, body)
}

// HistoryStreamCtlRequest contiene los atributos necesarios para ID 130.
type HistoryStreamCtlRequest struct {
	Operation ControlOperation
	MediaFlag string
	Speed     string
}

// NewHistoryStreamCtl valida y serializa HistoryStreamCtl (ID 130).
func NewHistoryStreamCtl(request HistoryStreamCtlRequest) (Message, error) {
	if request.Operation < ControlStartResume || request.Operation > ControlSpeed {
		return Message{}, fmt.Errorf("history: Operation debe ser 1, 2 o 3")
	}
	if err := require("MediaFlag", request.MediaFlag); err != nil {
		return Message{}, err
	}
	if request.Operation == ControlSpeed {
		if err := require("Speed", request.Speed); err != nil {
			return Message{}, err
		}
	}
	body := struct {
		XMLName   xml.Name         `xml:"HistoryStreamCtl"`
		Operation ControlOperation `xml:"Operation,attr"`
		MediaFlag string           `xml:"MediaFlag,attr"`
		Speed     string           `xml:"Speed,attr"`
	}{Operation: request.Operation, MediaFlag: request.MediaFlag, Speed: request.Speed}
	return marshalCommand(IDHistoryStreamCtl, body)
}
