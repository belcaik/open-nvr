package history

import (
	"encoding/xml"
	"fmt"
)

type replyElement struct {
	CmdReply  string `xml:"CmdReply,attr"`
	MediaFlag string `xml:"MediaFlag,attr"`
	Version   string `xml:"Version,attr"`
}

type replyDocument struct {
	XMLName              xml.Name
	ID                   uint16        `xml:"ID,attr"`
	HistoryStreamCreate  *replyElement `xml:"HistoryStreamCreateReply"`
	HistoryStreamDestroy *replyElement `xml:"HistoryStreamDestroyReply"`
	HistoryStreamPos     *replyElement `xml:"HistoryStreamPosReply"`
	HistoryStreamCtl     *replyElement `xml:"HistoryStreamCtlReply"`
	FileDownloadCreate   *replyElement `xml:"FileDownloadCreateReply"`
	FileDownloadRelease  *replyElement `xml:"FileDownloadReleaseReply"`
}

// ParseReply valida el ID, el elemento y los atributos obligatorios de una respuesta.
func ParseReply(message Message) (Reply, error) {
	var document replyDocument
	if err := decodeXML(message.Payload, &document); err != nil {
		return Reply{}, fmt.Errorf("history: XML de respuesta %d inválido: %w", message.ID, err)
	}
	if document.XMLName.Local != "Command" {
		return Reply{}, fmt.Errorf("history: raíz de respuesta %d debe ser Command", message.ID)
	}
	if document.ID != message.ID {
		return Reply{}, fmt.Errorf("history: ID XML %d no coincide con ID de mensaje %d", document.ID, message.ID)
	}

	var element *replyElement
	var name string
	switch message.ID {
	case IDHistoryStreamCreateReply:
		name, element = "HistoryStreamCreateReply", document.HistoryStreamCreate
	case IDHistoryStreamDestroyReply:
		name, element = "HistoryStreamDestroyReply", document.HistoryStreamDestroy
	case IDHistoryStreamPosReply:
		name, element = "HistoryStreamPosReply", document.HistoryStreamPos
	case IDHistoryStreamCtlReply:
		name, element = "HistoryStreamCtlReply", document.HistoryStreamCtl
	case IDFileDownloadCreateReply:
		name, element = "FileDownloadCreateReply", document.FileDownloadCreate
	case IDFileDownloadReleaseReply:
		name, element = "FileDownloadReleaseReply", document.FileDownloadRelease
	default:
		return Reply{}, fmt.Errorf("history: ID de respuesta no soportado %d", message.ID)
	}
	if element == nil {
		return Reply{}, fmt.Errorf("history: respuesta %d debe contener %s", message.ID, name)
	}
	if err := require("CmdReply", element.CmdReply); err != nil {
		return Reply{}, err
	}
	if element.CmdReply == "0" && (message.ID == IDHistoryStreamCreateReply || message.ID == IDFileDownloadCreateReply) {
		if err := require("MediaFlag", element.MediaFlag); err != nil {
			return Reply{}, err
		}
	}
	if message.ID == IDFileDownloadCreateReply {
		if err := require("Version", element.Version); err != nil {
			return Reply{}, err
		}
	}
	if message.ID == IDFileDownloadReleaseReply {
		if err := require("Version", element.Version); err != nil {
			return Reply{}, err
		}
	}
	reply := Reply{CmdReply: element.CmdReply}
	if message.ID == IDHistoryStreamCreateReply || message.ID == IDFileDownloadCreateReply {
		reply.MediaFlag = element.MediaFlag
		reply.Version = element.Version
	}
	if message.ID == IDHistoryStreamDestroyReply || message.ID == IDFileDownloadReleaseReply {
		reply.Version = element.Version
	}
	return reply, nil
}
