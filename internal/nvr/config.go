package nvr

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Valores admitidos, tomados de la interfaz web del propio NVR
// (captures/web/nvr/js/image/ispPage.js, lighting.html, sysinfsContrl.html).
var (
	FlipModes        = []string{"close", "leftright", "updown", "center"}
	WDRModes         = []string{"automatic", "weak", "normal", "strong", "close"}
	NoiseReduceModes = []string{"close", "weak", "normal", "strong"}
	BLCModes         = []string{"close", "open"}
	// Modos de visión nocturna y, para el modo "common", los de IR-CUT.
	IrCutModes = []string{"color", "intelligent", "variableinfraredlight", "variablewhitelight",
		"normal white", "passivity", "initiative", "day", "night", "time"}
	MotionSensitivities = []string{"high", "middle", "low"}
)

// ValidationError indica un valor inválido que no se envió al NVR.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("nvr: %s: %s", e.Field, e.Reason)
}

type ImageColor struct {
	Brightness int
	Contrast   int
	Saturation int
	Hue        int
	Sharpness  int
}

type imageColorXML struct {
	Brightness int `xml:"Brightness"`
	Contrast   int `xml:"Contrast"`
	Saturation int `xml:"Saturation"`
	Hue        int `xml:"Hue"`
	Sharpness  int `xml:"Sharpness"`
}

func (c *Client) ImageColor(ctx context.Context, ch int) (ImageColor, error) {
	path := fmt.Sprintf("/Images/%d/Color", ch)
	var x imageColorXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return ImageColor{}, err
	}
	return ImageColor(x), nil
}

func (c *Client) SetImageColor(ctx context.Context, ch int, v ImageColor) error {
	fields := []struct {
		name  string
		value int
	}{
		{"Brightness", v.Brightness}, {"Contrast", v.Contrast}, {"Saturation", v.Saturation},
		{"Hue", v.Hue}, {"Sharpness", v.Sharpness},
	}
	changes := make(map[string]string, len(fields))
	for _, field := range fields {
		if err := validateRange(field.name, field.value, 0, 100); err != nil {
			return err
		}
		changes[field.name] = strconv.Itoa(field.value)
	}
	return c.patchAndPut(ctx, fmt.Sprintf("/Images/%d/Color", ch), changes)
}

type ImageBasic struct {
	FlipMode    string
	WDR         string
	NoiseReduce string
	BLC         string
}

type imageBasicXML struct {
	FlipMode    string `xml:"FlipMode"`
	WDR         string `xml:"WDR"`
	NoiseReduce string `xml:"NoiseReduce"`
	BLC         string `xml:"BLC"`
}

func (c *Client) ImageBasic(ctx context.Context, ch int) (ImageBasic, error) {
	path := fmt.Sprintf("/Images/%d/Basic", ch)
	var x imageBasicXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return ImageBasic{}, err
	}
	return ImageBasic(x), nil
}

func (c *Client) SetImageBasic(ctx context.Context, ch int, v ImageBasic) error {
	if err := validateChoice("FlipMode", v.FlipMode, FlipModes...); err != nil {
		return err
	}
	if err := validateChoice("WDR", v.WDR, WDRModes...); err != nil {
		return err
	}
	if err := validateChoice("NoiseReduce", v.NoiseReduce, NoiseReduceModes...); err != nil {
		return err
	}
	if err := validateChoice("BLC", v.BLC, BLCModes...); err != nil {
		return err
	}
	return c.patchAndPut(ctx, fmt.Sprintf("/Images/%d/Basic", ch), map[string]string{
		"FlipMode": v.FlipMode, "WDR": v.WDR, "NoiseReduce": v.NoiseReduce, "BLC": v.BLC,
	})
}

type IrCut struct {
	Mode        string
	DayStart    string
	DayEnd      string
	Sensitivity int
}

type irCutXML struct {
	Mode        string `xml:"Mode"`
	DayStart    string `xml:"DayStartTime"`
	DayEnd      string `xml:"DayEndTime"`
	Sensitivity int    `xml:"Sensitivity"`
}

func (c *Client) IrCut(ctx context.Context, ch int) (IrCut, error) {
	path := fmt.Sprintf("/Images/%d/IrCutFilter", ch)
	var x irCutXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return IrCut{}, err
	}
	return IrCut(x), nil
}

func (c *Client) SetIrCut(ctx context.Context, ch int, v IrCut) error {
	if err := validateChoice("Mode", v.Mode, IrCutModes...); err != nil {
		return err
	}
	if err := validateClock("DayStart", v.DayStart); err != nil {
		return err
	}
	if err := validateClock("DayEnd", v.DayEnd); err != nil {
		return err
	}
	if err := validateRange("Sensitivity", v.Sensitivity, 0, 100); err != nil {
		return err
	}
	return c.patchAndPut(ctx, fmt.Sprintf("/Images/%d/IrCutFilter", ch), map[string]string{
		"Mode": v.Mode, "DayStartTime": v.DayStart, "DayEndTime": v.DayEnd,
		"Sensitivity": strconv.Itoa(v.Sensitivity),
	})
}

type RecordSchedule struct {
	StreamType string
	AllDay     bool
	Days       [7][48]bool
}

type recordScheduleXML struct {
	StreamType string `xml:"StreamType"`
	Schedule   struct {
		AllDay string `xml:"AllDay"`
		Blocks struct {
			Day0 string `xml:"TimeBlock_0"`
			Day1 string `xml:"TimeBlock_1"`
			Day2 string `xml:"TimeBlock_2"`
			Day3 string `xml:"TimeBlock_3"`
			Day4 string `xml:"TimeBlock_4"`
			Day5 string `xml:"TimeBlock_5"`
			Day6 string `xml:"TimeBlock_6"`
		} `xml:"TimeBlockList"`
	} `xml:"Schedule"`
}

func (c *Client) RecordSchedule(ctx context.Context, ch int) (RecordSchedule, error) {
	path := fmt.Sprintf("/Record/%d/RecordSchedule", ch)
	var x recordScheduleXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return RecordSchedule{}, err
	}
	values := [...]string{x.Schedule.Blocks.Day0, x.Schedule.Blocks.Day1, x.Schedule.Blocks.Day2, x.Schedule.Blocks.Day3, x.Schedule.Blocks.Day4, x.Schedule.Blocks.Day5, x.Schedule.Blocks.Day6}
	result := RecordSchedule{StreamType: x.StreamType, AllDay: x.Schedule.AllDay == "true"}
	allEnabled := true
	for day, value := range values {
		if len(value) != 48 {
			return RecordSchedule{}, &BadResponseError{Path: path, HTTP: http.StatusOK}
		}
		for halfHour, bit := range []byte(value) {
			if bit != '0' && bit != '1' {
				return RecordSchedule{}, &BadResponseError{Path: path, HTTP: http.StatusOK}
			}
			result.Days[day][halfHour] = bit == '1'
			allEnabled = allEnabled && bit == '1'
		}
	}
	// Algunos firmwares informan AllDay=false aun con las 336 franjas activas.
	result.AllDay = result.AllDay || allEnabled
	return result, nil
}

func (c *Client) SetRecordSchedule(ctx context.Context, ch int, v RecordSchedule) error {
	if err := validateChoice("StreamType", v.StreamType, "main", "sub"); err != nil {
		return err
	}
	changes := map[string]string{
		"StreamType":      v.StreamType,
		"Schedule/AllDay": strconv.FormatBool(v.AllDay),
	}
	for day, slots := range v.Days {
		var value strings.Builder
		value.Grow(len(slots))
		for _, enabled := range slots {
			if enabled {
				value.WriteByte('1')
			} else {
				value.WriteByte('0')
			}
		}
		changes[fmt.Sprintf("Schedule/TimeBlockList/TimeBlock_%d", day)] = value.String()
	}
	return c.patchAndPut(ctx, fmt.Sprintf("/Record/%d/RecordSchedule", ch), changes)
}

type RecordSettings struct {
	PreRecord   int
	DelayRecord int
	StreamType  string
	Overwrite   bool
}

type recordSettingsXML struct {
	PreRecord       int    `xml:"PreRecord"`
	DelayRecord     int    `xml:"DelayRecord"`
	StreamType      string `xml:"RecordStreamType"`
	WriteCircleType string `xml:"RecordWriteCircleType"`
}

func (c *Client) RecordSettings(ctx context.Context) (RecordSettings, error) {
	const path = "/Record"
	var x recordSettingsXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return RecordSettings{}, err
	}
	return RecordSettings{PreRecord: x.PreRecord, DelayRecord: x.DelayRecord, StreamType: x.StreamType, Overwrite: x.WriteCircleType == "writecircle"}, nil
}

func (c *Client) SetRecordSettings(ctx context.Context, v RecordSettings) error {
	if err := validateRange("PreRecord", v.PreRecord, 0, 30); err != nil {
		return err
	}
	if err := validateRange("DelayRecord", v.DelayRecord, 0, 300); err != nil {
		return err
	}
	if err := validateChoice("StreamType", v.StreamType, "main", "sub"); err != nil {
		return err
	}
	writeCircle := "fullnotwrite"
	if v.Overwrite {
		writeCircle = "writecircle"
	}
	return c.patchAndPut(ctx, "/Record", map[string]string{
		"PreRecord": strconv.Itoa(v.PreRecord), "DelayRecord": strconv.Itoa(v.DelayRecord),
		"RecordStreamType": v.StreamType, "RecordWriteCircleType": writeCircle,
	})
}

type Clock struct {
	Now        time.Time
	NTPEnabled bool
	NTPServer  string
	TimeZone   string
	LastSync   string
}

type timeXML struct {
	SystemTime string `xml:"SystemTime"`
}

type ntpXML struct {
	Enabled  bool   `xml:"Enable"`
	Server   string `xml:"ServerName"`
	TimeZone string `xml:"TimeZone"`
	LastSync string `xml:"LastSuccessSyncNTPTime"`
}

func (c *Client) Clock(ctx context.Context) (Clock, error) {
	var current timeXML
	if err := c.getXML(ctx, "/System/Time", &current); err != nil {
		return Clock{}, err
	}
	loc := c.loc
	if loc == nil {
		loc = time.Local
	}
	now, err := time.ParseInLocation("20060102T150405", current.SystemTime, loc)
	if err != nil {
		return Clock{}, &BadResponseError{Path: "/System/Time", HTTP: http.StatusOK}
	}
	var ntp ntpXML
	if err := c.getXML(ctx, "/System/NTP", &ntp); err != nil {
		return Clock{}, err
	}
	return Clock{Now: now, NTPEnabled: ntp.Enabled, NTPServer: ntp.Server, TimeZone: ntp.TimeZone, LastSync: ntp.LastSync}, nil
}

func (c *Client) SyncNTP(ctx context.Context) error {
	const path = "/System/NTP"
	doc, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return err
	}
	patched, err := PatchXML(doc, map[string]string{"IsSynchronizeNTPNow": "true"})
	if err != nil {
		if bytes.Contains(doc, []byte("<IsSynchronizeNTPNow")) {
			return err
		}
		var current ntpXML
		if xml.Unmarshal(doc, &current) != nil {
			return err
		}
		needle := []byte("    <SynchronizeNTPNowStatus>")
		if bytes.Count(doc, needle) != 1 {
			return err
		}
		patched = bytes.Replace(doc, needle, []byte("    <IsSynchronizeNTPNow>true</IsSynchronizeNTPNow>\n    <SynchronizeNTPNowStatus>"), 1)
	}
	_, err = c.do(ctx, http.MethodPut, path, string(patched), controlTimeout)
	return err
}

type Disk struct {
	ID      int
	Model   string
	TotalMB int
	FreeMB  int
	State   string
}

type diskListXML struct {
	Disks []struct {
		ID      int    `xml:"DiskID"`
		Model   string `xml:"DiskTypeName"`
		TotalMB int    `xml:"TotalCapacity"`
		FreeMB  int    `xml:"AvailableCapacity"`
		State   string `xml:"DiskStorageAttribute"`
	} `xml:"Disk"`
}

func (c *Client) Disks(ctx context.Context) ([]Disk, error) {
	const path = "/Disk"
	var x diskListXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return nil, err
	}
	disks := make([]Disk, len(x.Disks))
	for i, disk := range x.Disks {
		disks[i] = Disk(disk)
	}
	return disks, nil
}

type NetworkInfo struct {
	IP      string
	Mask    string
	Gateway string
	DNS1    string
	DNS2    string
	MAC     string
	DHCP    bool
}

type networkXML struct {
	Interfaces []struct {
		Address struct {
			IP      string `xml:"ipAddress"`
			Mask    string `xml:"SubnetMask"`
			Gateway string `xml:"Gateway"`
			MAC     string `xml:"MacAddress"`
			DHCP    struct {
				Enable bool `xml:"Enable"`
			} `xml:"DHCP"`
			DNS struct {
				Primary   string `xml:"PrimaryDNS"`
				Secondary string `xml:"SecondaryDNS"`
			} `xml:"DNS"`
		} `xml:"IPAddress"`
	} `xml:"Interface"`
}

func (c *Client) Network(ctx context.Context) (NetworkInfo, error) {
	const path = "/Network/Interfaces"
	var x networkXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return NetworkInfo{}, err
	}
	if len(x.Interfaces) == 0 {
		return NetworkInfo{}, &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	a := x.Interfaces[0].Address
	return NetworkInfo{IP: a.IP, Mask: a.Mask, Gateway: a.Gateway, DNS1: a.DNS.Primary, DNS2: a.DNS.Secondary, MAC: a.MAC, DHCP: a.DHCP.Enable}, nil
}

type Motion struct {
	Enabled     bool
	Sensitivity string
}

type motionXML struct {
	Enabled     bool   `xml:"Enable"`
	Sensitivity string `xml:"Senstive"`
}

func (c *Client) Motion(ctx context.Context, ch int) (Motion, error) {
	path := fmt.Sprintf("/Pictures/%d/Motion", ch)
	var x motionXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return Motion{}, err
	}
	return Motion(x), nil
}

func (c *Client) SetMotion(ctx context.Context, ch int, v Motion) error {
	if err := validateChoice("Sensitivity", v.Sensitivity, MotionSensitivities...); err != nil {
		return err
	}
	return c.patchAndPut(ctx, fmt.Sprintf("/Pictures/%d/Motion", ch), map[string]string{
		"Enable": strconv.FormatBool(v.Enabled), "Senstive": v.Sensitivity,
	})
}

type StreamInfo struct {
	Codec       string
	Width       int
	Height      int
	FPS         int
	BitrateKbps int
	BitrateType string
}

type streamXML struct {
	Codec       string `xml:"VideoCodecType"`
	Width       int    `xml:"ResolutionWidth"`
	Height      int    `xml:"ResolutionHeigth"`
	FrameRate   int    `xml:"FrameRate"`
	BitrateKbps int    `xml:"Bitrate"`
	BitrateType string `xml:"BitrateType"`
}

func (c *Client) Stream(ctx context.Context, ch, stream int) (StreamInfo, error) {
	path := fmt.Sprintf("/Streams/%d/%d", ch, stream)
	var x streamXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return StreamInfo{}, err
	}
	return StreamInfo{Codec: x.Codec, Width: x.Width, Height: x.Height, FPS: x.FrameRate / 100, BitrateKbps: x.BitrateKbps, BitrateType: x.BitrateType}, nil
}

func (c *Client) getXML(ctx context.Context, path string, dst any) error {
	b, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(b, dst); err != nil {
		return &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	return nil
}

func (c *Client) patchAndPut(ctx context.Context, path string, changes map[string]string) error {
	doc, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return err
	}
	patched, err := PatchXML(doc, changes)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, path, string(patched), controlTimeout)
	return err
}

func validateRange(field string, value, minValue, maxValue int) error {
	if value < minValue || value > maxValue {
		return &ValidationError{Field: field, Reason: fmt.Sprintf("debe estar entre %d y %d", minValue, maxValue)}
	}
	return nil
}

func validateChoice(field, value string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return &ValidationError{Field: field, Reason: fmt.Sprintf("valor %q no admitido", value)}
}

func validateClock(field, value string) error {
	if _, err := time.Parse("15:04:05", value); err != nil {
		return &ValidationError{Field: field, Reason: "debe tener formato HH:MM:SS"}
	}
	return nil
}
