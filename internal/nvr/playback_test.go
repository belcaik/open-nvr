package nvr

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRecordDaysOctober27MarksOnlyThe27th(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByMonth", fixtureDir+"/config/Playback_GetRecordInfoByMonth_20261027.xml")

	got, err := c.RecordDays(context.Background(), 2026, time.October)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{27}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordDays = %v, quiero %v", got, want)
	}
	req := f.last(t, "GET", "/Playback/GetRecordInfoByMonth")
	if req.uri != "/Playback/GetRecordInfoByMonth?Year=2026&Month=10" {
		t.Fatalf("uri = %q", req.uri)
	}
}

func TestRecordDaysOctoberMarks26To28(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByMonth", fixtureDir+"/config/Playback_GetRecordInfoByMonth.xml")

	got, err := c.RecordDays(context.Background(), 2026, time.October)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{26, 27, 28}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordDays = %v, quiero %v", got, want)
	}
	req := f.last(t, "GET", "/Playback/GetRecordInfoByMonth")
	if req.uri != "/Playback/GetRecordInfoByMonth?Year=2026&Month=10" {
		t.Fatalf("uri = %q", req.uri)
	}
}

func santiagoLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestRecordSegmentsChannel1Day28SortedInSantiago(t *testing.T) {
	f, c := newFakeNVR(t)
	loc := santiagoLoc(t)
	c.WithLocation(loc)
	f.setRoute("GET /Playback/GetRecordInfoByDay", fixtureDir+"/config/Playback_GetRecordInfoByDay.xml")

	day := time.Date(2026, 10, 28, 12, 0, 0, 0, loc)
	got, err := c.RecordSegments(context.Background(), 1, day)
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{Start: time.Date(2026, 10, 28, 0, 0, 0, 0, loc), End: time.Date(2026, 10, 28, 0, 10, 36, 0, loc), Type: "general"},
		{Start: time.Date(2026, 10, 28, 0, 10, 36, 0, loc), End: time.Date(2026, 10, 28, 0, 11, 24, 0, loc), Type: "ai"},
		{Start: time.Date(2026, 10, 28, 0, 11, 24, 0, loc), End: time.Date(2026, 10, 28, 0, 41, 18, 0, loc), Type: "general"},
		{Start: time.Date(2026, 10, 28, 0, 41, 18, 0, loc), End: time.Date(2026, 10, 28, 0, 42, 8, 0, loc), Type: "ai"},
		{Start: time.Date(2026, 10, 28, 0, 42, 8, 0, loc), End: time.Date(2026, 10, 28, 0, 55, 59, 0, loc), Type: "general"},
		{Start: time.Date(2026, 10, 28, 0, 55, 59, 0, loc), End: time.Date(2026, 10, 28, 0, 56, 45, 0, loc), Type: "ai"},
		{Start: time.Date(2026, 10, 28, 0, 56, 45, 0, loc), End: time.Date(2026, 10, 28, 1, 2, 36, 0, loc), Type: "general"},
		{Start: time.Date(2026, 10, 28, 1, 2, 36, 0, loc), End: time.Date(2026, 10, 28, 1, 3, 24, 0, loc), Type: "ai"},
		{Start: time.Date(2026, 10, 28, 1, 3, 24, 0, loc), End: time.Date(2026, 10, 28, 1, 8, 8, 0, loc), Type: "general"},
	}
	if len(got) != len(want) {
		t.Fatalf("segmentos = %d, quiero %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Type != want[i].Type {
			t.Errorf("segs[%d].Type = %q, quiero %q", i, got[i].Type, want[i].Type)
		}
		if !got[i].Start.Equal(want[i].Start) || got[i].Start.Location() != loc {
			t.Errorf("segs[%d].Start = %v (%s), quiero %v (America/Santiago)", i, got[i].Start, got[i].Start.Location(), want[i].Start)
		}
		if !got[i].End.Equal(want[i].End) || got[i].End.Location() != loc {
			t.Errorf("segs[%d].End = %v (%s), quiero %v (America/Santiago)", i, got[i].End, got[i].End.Location(), want[i].End)
		}
	}
	req := f.last(t, "GET", "/Playback/GetRecordInfoByDay")
	if req.uri != "/Playback/GetRecordInfoByDay?Channel=1&RecordType=255&Year=2026&Month=10&Day=28" {
		t.Fatalf("uri = %q", req.uri)
	}
}

func TestRecordSegmentsMergesSameTypeGapsOfTwoSecondsOrLess(t *testing.T) {
	f, c := newFakeNVR(t)
	loc := santiagoLoc(t)
	c.WithLocation(loc)
	// Fixture sintético: desordenado a propósito. Hueco de 2 s se une; de 3 s no.
	// Un tramo ai pegado al general no se une (tipo distinto).
	f.mu.Lock()
	f.raw["GET /Playback/GetRecordInfoByDay"] = []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<RecordInfoByDay Version="1.0">
    <RecordInfoList Version="1.0">
        <RecordInfo Version="1.0">
            <Channel>0</Channel>
            <RecordType>general</RecordType>
            <StartTime>20261028002003</StartTime>
            <EndTime>20261028003000</EndTime>
        </RecordInfo>
        <RecordInfo Version="1.0">
            <Channel>0</Channel>
            <RecordType>ai</RecordType>
            <StartTime>20261028003000</StartTime>
            <EndTime>20261028003010</EndTime>
        </RecordInfo>
        <RecordInfo Version="1.0">
            <Channel>0</Channel>
            <RecordType>general</RecordType>
            <StartTime>20261028001002</StartTime>
            <EndTime>20261028002000</EndTime>
        </RecordInfo>
        <RecordInfo Version="1.0">
            <Channel>0</Channel>
            <RecordType>general</RecordType>
            <StartTime>20261028000000</StartTime>
            <EndTime>20261028001000</EndTime>
        </RecordInfo>
    </RecordInfoList>
</RecordInfoByDay>`)
	f.mu.Unlock()

	got, err := c.RecordSegments(context.Background(), 1, time.Date(2026, 10, 28, 0, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{Start: time.Date(2026, 10, 28, 0, 0, 0, 0, loc), End: time.Date(2026, 10, 28, 0, 20, 0, 0, loc), Type: "general"},
		{Start: time.Date(2026, 10, 28, 0, 20, 3, 0, loc), End: time.Date(2026, 10, 28, 0, 30, 0, 0, loc), Type: "general"},
		{Start: time.Date(2026, 10, 28, 0, 30, 0, 0, loc), End: time.Date(2026, 10, 28, 0, 30, 10, 0, loc), Type: "ai"},
	}
	if len(got) != len(want) {
		t.Fatalf("segmentos = %d, quiero %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Type != want[i].Type || !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) {
			t.Errorf("segs[%d] = {%v %v %q}, quiero {%v %v %q}", i,
				got[i].Start, got[i].End, got[i].Type,
				want[i].Start, want[i].End, want[i].Type)
		}
	}
}

func TestRecordDaysJanuaryQueryHasNoLeadingZero(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByMonth", fixtureDir+"/config/Playback_GetRecordInfoByMonth_20261027.xml")
	if _, err := c.RecordDays(context.Background(), 2026, time.January); err != nil {
		t.Fatal(err)
	}
	req := f.last(t, "GET", "/Playback/GetRecordInfoByMonth")
	if req.uri != "/Playback/GetRecordInfoByMonth?Year=2026&Month=1" {
		t.Fatalf("uri = %q, Month debe ir sin cero inicial", req.uri)
	}
}

func TestRecordSegmentsEmptyList(t *testing.T) {
	f, c := newFakeNVR(t)
	f.mu.Lock()
	f.raw["GET /Playback/GetRecordInfoByDay"] = []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<RecordInfoByDay Version="1.0">
    <RecordInfoList Version="1.0">
    </RecordInfoList>
</RecordInfoByDay>`)
	f.mu.Unlock()
	got, err := c.RecordSegments(context.Background(), 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("segmentos = %v, quiero lista vacía", got)
	}
}

func TestRecordSegmentsEmptyResponseStatusIsEmptyList(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByDay", fixtureDir+"/ok.xml")
	got, err := c.RecordSegments(context.Background(), 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("segmentos = %v, quiero lista vacía", got)
	}
}

func TestRecordSegmentsStatusMinus1IsBadResponse(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByDay", fixtureDir+"/status_minus1.xml")
	_, err := c.RecordSegments(context.Background(), 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	var bre *BadResponseError
	if !errors.As(err, &bre) {
		t.Fatalf("err = %v (%T), quiero *BadResponseError", err, err)
	}
	if bre.Status != "-1" {
		t.Fatalf("Status = %q, quiero %q", bre.Status, "-1")
	}
}

func TestRecordDaysStatusMinus1IsBadResponse(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByMonth", fixtureDir+"/status_minus1.xml")
	_, err := c.RecordDays(context.Background(), 2026, time.October)
	var bre *BadResponseError
	if !errors.As(err, &bre) {
		t.Fatalf("err = %v (%T), quiero *BadResponseError", err, err)
	}
	if bre.Status != "-1" {
		t.Fatalf("Status = %q, quiero %q", bre.Status, "-1")
	}
}

func TestRecordSegmentsDayOneQueryHasNoLeadingZero(t *testing.T) {
	f, c := newFakeNVR(t)
	f.mu.Lock()
	f.raw["GET /Playback/GetRecordInfoByDay"] = []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<RecordInfoByDay Version="1.0"><RecordInfoList Version="1.0"></RecordInfoList></RecordInfoByDay>`)
	f.mu.Unlock()
	if _, err := c.RecordSegments(context.Background(), 1, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	req := f.last(t, "GET", "/Playback/GetRecordInfoByDay")
	if req.uri != "/Playback/GetRecordInfoByDay?Channel=1&RecordType=255&Year=2026&Month=1&Day=1" {
		t.Fatalf("uri = %q, mes y día deben ir sin cero inicial", req.uri)
	}
}

func TestRecordDaysEmptyResponseStatusIsEmptyList(t *testing.T) {
	f, c := newFakeNVR(t)
	f.setRoute("GET /Playback/GetRecordInfoByMonth", fixtureDir+"/ok.xml")
	got, err := c.RecordDays(context.Background(), 2026, time.January)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("RecordDays = %v, quiero lista vacía", got)
	}
}

func TestRecordDaysEmptyBitmap(t *testing.T) {
	f, c := newFakeNVR(t)
	f.mu.Lock()
	f.raw["GET /Playback/GetRecordInfoByMonth"] = []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<RecordInfoByMonth Version="1.0">
    <RecordInfo>00000000000000000000000000000000</RecordInfo>
</RecordInfoByMonth>`)
	f.mu.Unlock()
	got, err := c.RecordDays(context.Background(), 2026, time.January)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("RecordDays = %v, quiero lista vacía", got)
	}
}
