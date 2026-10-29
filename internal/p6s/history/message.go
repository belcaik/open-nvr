// Package history serializa comandos P6S de reproducción y descarga sin
// encuadre de transporte.
package history

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
)

const wireXMLProlog = `<?xml version="1.0" encoding="GB2312" standalone="yes" ?>` + "\n"

const (
	IDFileDownloadCreate           uint16 = 56
	IDFileDownloadCreateReply      uint16 = 57
	IDFileDownloadRelease          uint16 = 59
	IDFileDownloadReleaseReply     uint16 = 60
	IDHistoryStreamCreate          uint16 = 124
	IDHistoryStreamCreateMultiType uint16 = 185
	IDHistoryStreamCreateReply     uint16 = 125
	IDHistoryStreamDestroy         uint16 = 126
	IDHistoryStreamDestroyReply    uint16 = 127
	IDHistoryStreamPos             uint16 = 128
	IDHistoryStreamPosReply        uint16 = 129
	IDHistoryStreamCtl             uint16 = 130
	IDHistoryStreamCtlReply        uint16 = 131
)

// Message contiene el identificador P6S y su documento XML.
type Message struct {
	ID      uint16
	Payload []byte
}

// Reply contiene los atributos comunes de las respuestas de este paquete.
type Reply struct {
	CmdReply  string
	MediaFlag string
	Version   string
}

type command struct {
	XMLName xml.Name `xml:"Command"`
	ID      uint16   `xml:"ID,attr"`
	Body    any
}

func marshalCommand(id uint16, body any) (Message, error) {
	payload, err := xml.Marshal(command{ID: id, Body: body})
	if err != nil {
		return Message{}, fmt.Errorf("history: serializar comando %d: %w", id, err)
	}
	return Message{ID: id, Payload: payload}, nil
}

func wirePayload(document []byte) []byte {
	payload := make([]byte, 0, len(wireXMLProlog)+len(document)+1)
	payload = append(payload, wireXMLProlog...)
	payload = append(payload, document...)
	payload = append(payload, 0)
	return payload
}

func decodeXML(payload []byte, value any) error {
	payload = bytes.TrimRight(payload, "\x00")
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	return decoder.Decode(value)
}

func require(field, value string) error {
	if value == "" {
		return fmt.Errorf("history: %s es obligatorio", field)
	}
	return nil
}
