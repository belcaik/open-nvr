package nvr

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Segment es un tramo grabado. Start y End van en la zona del cliente (c.loc).
type Segment struct {
	Start, End time.Time
	Type       string // general|manual|alarm|motion|ai (texto crudo)
}

type recordInfoByMonthXML struct {
	XMLName    xml.Name `xml:"RecordInfoByMonth"`
	RecordInfo string   `xml:"RecordInfo"`
}

// emptyPlaybackReply es un ResponseStatus con statusCode 0: el NVR no tiene
// grabaciones. statusCode distinto de 0 ya lo rechazó c.do.
func emptyPlaybackReply(b []byte) bool {
	var rs responseStatus
	return xml.Unmarshal(b, &rs) == nil && rs.XMLName.Local == "ResponseStatus"
}

// RecordDays devuelve los días (1..31) con grabación en ese mes, en
// cualquier canal. El NVR responde un bitmap de 32 caracteres en
// <RecordInfo>: el carácter i (base 0) es el día i+1.
func (c *Client) RecordDays(ctx context.Context, year int, month time.Month) ([]int, error) {
	path := fmt.Sprintf("/Playback/GetRecordInfoByMonth?Year=%d&Month=%d", year, month)
	b, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return nil, err
	}
	if emptyPlaybackReply(b) {
		return nil, nil
	}
	var x recordInfoByMonthXML
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil, &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	info := strings.TrimSpace(x.RecordInfo)
	var days []int
	for i := 0; i < len(info); i++ {
		if info[i] == '1' {
			days = append(days, i+1)
		}
	}
	return days, nil
}

type recordInfoByDayXML struct {
	XMLName xml.Name `xml:"RecordInfoByDay"`
	List    []struct {
		RecordType string `xml:"RecordType"`
		StartTime  string `xml:"StartTime"`
		EndTime    string `xml:"EndTime"`
	} `xml:"RecordInfoList>RecordInfo"`
}

const nvrRecordTimeLayout = "20060102150405"

func (c *Client) location() *time.Location {
	if c.loc != nil {
		return c.loc
	}
	return time.Local
}

// RecordSegments devuelve los tramos grabados de un canal (1..N) en un día,
// ordenados por Start. Une tramos contiguos del mismo tipo si el hueco es
// de 2 s o menos.
// GET /Playback/GetRecordInfoByDay?Channel={ch}&RecordType=255&Year=&Month=&Day=
// (mes y día sin cero inicial). El parámetro Channel es base 1; en la
// respuesta <Channel> es base 0.
func (c *Client) RecordSegments(ctx context.Context, ch int, day time.Time) ([]Segment, error) {
	path := fmt.Sprintf("/Playback/GetRecordInfoByDay?Channel=%d&RecordType=255&Year=%d&Month=%d&Day=%d",
		ch, day.Year(), day.Month(), day.Day())
	b, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return nil, err
	}
	if emptyPlaybackReply(b) {
		return nil, nil
	}
	var x recordInfoByDayXML
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil, &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	loc := c.location()
	segs := make([]Segment, 0, len(x.List))
	for _, r := range x.List {
		start, err := time.ParseInLocation(nvrRecordTimeLayout, r.StartTime, loc)
		if err != nil {
			return nil, &BadResponseError{Path: path, HTTP: http.StatusOK}
		}
		end, err := time.ParseInLocation(nvrRecordTimeLayout, r.EndTime, loc)
		if err != nil {
			return nil, &BadResponseError{Path: path, HTTP: http.StatusOK}
		}
		segs = append(segs, Segment{Start: start, End: end, Type: r.RecordType})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start.Before(segs[j].Start) })
	return mergeSegments(segs), nil
}

// mergeSegments une tramos del mismo tipo cuyo hueco (Start del siguiente
// menos End del actual) es de 2 s o menos, incluido solape.
func mergeSegments(segs []Segment) []Segment {
	if len(segs) == 0 {
		return segs
	}
	out := []Segment{segs[0]}
	for _, s := range segs[1:] {
		last := &out[len(out)-1]
		if s.Type == last.Type && !s.Start.After(last.End.Add(2*time.Second)) {
			if s.End.After(last.End) {
				last.End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}
