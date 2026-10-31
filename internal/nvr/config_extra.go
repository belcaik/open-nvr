package nvr

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type P2P struct {
	Enabled bool
	UUID    string
	State   string
}

type p2pListXML struct {
	Entries []struct {
		Enabled  bool   `xml:"Enable"`
		Protocol string `xml:"P2PProtocolName"`
		UUID     string `xml:"UUID"`
		State    string `xml:"State"`
		Status   string `xml:"Status"`
	} `xml:"P2P"`
}

func (c *Client) P2P(ctx context.Context) (P2P, error) {
	const path = "/Network/P2PV2"
	var x p2pListXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return P2P{}, err
	}
	for _, entry := range x.Entries {
		if entry.Protocol == "tutk" {
			state := entry.State
			if state == "" {
				state = entry.Status
			}
			return P2P{Enabled: entry.Enabled, UUID: entry.UUID, State: state}, nil
		}
	}
	return P2P{}, &BadResponseError{Path: path, HTTP: http.StatusOK}
}

func (c *Client) SetP2P(ctx context.Context, enabled bool) error {
	const path = "/Network/P2PV2"
	doc, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return err
	}
	patched, err := patchP2PEntries(doc, enabled)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPut, path, string(patched), controlTimeout)
	return err
}

// patchP2PEntries reproduce la semántica de la UI original: desactiva todas
// las entradas y activa únicamente tutk. Cada P2P se parchea con PatchXML,
// por lo que atributos y campos desconocidos permanecen byte a byte.
func patchP2PEntries(doc []byte, enabled bool) ([]byte, error) {
	type replacement struct {
		start, end int
		data       []byte
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var depth int
	var start int64 = -1
	var replacements []replacement
	foundTutk := false
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("nvr: XML P2P inválido: %w", err)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if tok.Name.Local == "P2P" && depth == 0 {
				start = before
			}
			if start >= 0 {
				depth++
			}
		case xml.EndElement:
			if start >= 0 {
				depth--
				if depth == 0 && tok.Name.Local == "P2P" {
					end := dec.InputOffset()
					fragment := doc[start:end]
					var entry struct {
						Protocol string `xml:"P2PProtocolName"`
					}
					if err := xml.Unmarshal(fragment, &entry); err != nil {
						return nil, fmt.Errorf("nvr: entrada P2P inválida: %w", err)
					}
					value := false
					if entry.Protocol == "tutk" {
						foundTutk = true
						value = enabled
					}
					p, err := PatchXML(fragment, map[string]string{"Enable": strconv.FormatBool(value)})
					if err != nil {
						return nil, err
					}
					replacements = append(replacements, replacement{int(start), int(end), p})
					start = -1
				}
			}
		}
	}
	if !foundTutk {
		return nil, fmt.Errorf("nvr: no existe la entrada P2P tutk")
	}
	patched := append([]byte(nil), doc...)
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		out := make([]byte, 0, len(patched)-(r.end-r.start)+len(r.data))
		out = append(out, patched[:r.start]...)
		out = append(out, r.data...)
		out = append(out, patched[r.end:]...)
		patched = out
	}
	return patched, nil
}

type AudioStream struct {
	InputMethod     string
	Codec           string
	InputVolume     int
	OutputVolume    int
	HasOutputVolume bool
}

type audioStreamXML struct {
	InputMethod  string `xml:"AudioInMethod"`
	Codec        string `xml:"AudioInCodec"`
	InputVolume  int    `xml:"AudioInVolume"`
	OutputVolume *int   `xml:"AudioOutVolume"`
}

func (c *Client) AudioStream(ctx context.Context) (AudioStream, error) {
	const path = "/Streams/AudioStream"
	var x audioStreamXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return AudioStream{}, err
	}
	v := AudioStream{InputMethod: x.InputMethod, Codec: x.Codec, InputVolume: x.InputVolume, HasOutputVolume: x.OutputVolume != nil}
	if x.OutputVolume != nil {
		v.OutputVolume = *x.OutputVolume
	}
	return v, nil
}

func (c *Client) SetAudioStream(ctx context.Context, v AudioStream) error {
	if err := validateChoice("AudioInMethod", v.InputMethod, AudioInputMethods...); err != nil {
		return err
	}
	if err := validateChoice("AudioInCodec", v.Codec, AudioCodecs...); err != nil {
		return err
	}
	if err := validateRange("AudioInVolume", v.InputVolume, 0, 100); err != nil {
		return err
	}
	changes := map[string]string{
		"AudioInMethod": v.InputMethod, "AudioInCodec": v.Codec,
		"AudioInVolume": strconv.Itoa(v.InputVolume),
	}
	if v.HasOutputVolume {
		if err := validateRange("AudioOutVolume", v.OutputVolume, 0, 100); err != nil {
			return err
		}
		changes["AudioOutVolume"] = strconv.Itoa(v.OutputVolume)
	}
	return c.patchAndPut(ctx, "/Streams/AudioStream", changes)
}

var LogMajorTypes = []string{"1", "2", "3", "4", "6", "7", "8", "9"}

type LogEntry struct {
	Time          string
	User          string
	HostIP        string
	MinorType     string
	Channel       string
	IPConflictMAC string
}

type logListXML struct {
	Entries []struct {
		Time          string `xml:"time"`
		User          string `xml:"netUser"`
		HostIP        string `xml:"hostIPAddress"`
		MinorType     string `xml:"minorType"`
		Channel       string `xml:"channel"`
		IPConflictMAC string `xml:"IpConflictMac"`
	} `xml:"LogList>Log"`
}

func (c *Client) Logs(ctx context.Context, majorType string, start, stop time.Time) ([]LogEntry, error) {
	if err := validateChoice("majorType", majorType, LogMajorTypes...); err != nil {
		return nil, err
	}
	if stop.Before(start) || stop.Sub(start) > 31*24*time.Hour {
		return nil, &ValidationError{Field: "rango", Reason: "debe estar ordenado y no superar 31 días"}
	}
	q := url.Values{
		"majorType": {majorType}, "minorType": {"0"},
		"startTime": {start.Format("2006-01-02T15:04:05")},
		"stopTime":  {stop.Format("2006-01-02T15:04:05")},
	}
	path := "/System/Logging?" + q.Encode()
	var x logListXML
	if err := c.getXML(ctx, path, &x); err != nil {
		return nil, err
	}
	if len(x.Entries) > 1000 {
		x.Entries = x.Entries[:1000]
	}
	entries := make([]LogEntry, len(x.Entries))
	for i, entry := range x.Entries {
		entries[i] = LogEntry(entry)
	}
	return entries, nil
}
