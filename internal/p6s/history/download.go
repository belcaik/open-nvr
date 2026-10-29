package history

import "encoding/xml"

// FileDownloadCreateRequest contiene los atributos necesarios para ID 56.
type FileDownloadCreateRequest struct {
	FileType string
	FileName string
}

// NewFileDownloadCreate valida y serializa FileDownloadCreate (ID 56).
func NewFileDownloadCreate(request FileDownloadCreateRequest) (Message, error) {
	if err := require("FileType", request.FileType); err != nil {
		return Message{}, err
	}
	if err := require("FileName", request.FileName); err != nil {
		return Message{}, err
	}
	body := struct {
		XMLName  xml.Name `xml:"FileDownloadCreate"`
		FileType string   `xml:"FileType,attr"`
		FileName string   `xml:"FileName,attr"`
	}{FileType: request.FileType, FileName: request.FileName}
	return marshalCommand(IDFileDownloadCreate, body)
}

// FileDownloadReleaseRequest contiene los atributos necesarios para ID 59.
type FileDownloadReleaseRequest struct {
	MediaFlag string
}

// NewFileDownloadRelease valida y serializa FileDownloadRelease (ID 59).
func NewFileDownloadRelease(request FileDownloadReleaseRequest) (Message, error) {
	if err := require("MediaFlag", request.MediaFlag); err != nil {
		return Message{}, err
	}
	body := struct {
		XMLName   xml.Name `xml:"FileDownloadRelease"`
		MediaFlag string   `xml:"MediaFlag,attr"`
	}{MediaFlag: request.MediaFlag}
	return marshalCommand(IDFileDownloadRelease, body)
}
