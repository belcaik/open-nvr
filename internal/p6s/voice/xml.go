package voice

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	VoiceStreamCreateRequestID  = 148
	VoiceStreamCreateReplyID    = 149
	VoiceStreamStartRequestID   = 150
	VoiceStreamStartReplyID     = 151
	VoiceStreamStopRequestID    = 152
	VoiceStreamStopReplyID      = 153
	VoiceStreamDestroyRequestID = 154
	VoiceStreamDestroyReplyID   = 155
)

// ErrNegativeChannel rechaza un canal menor que 0. El canal P6S es base 0:
// el primer canal es 0.
var ErrNegativeChannel = errors.New("canal negativo")

// ErrResult rechaza un Result o CmdReply ausente o no entero.
var ErrResult = errors.New("resultado inválido")

// Command es un documento VoiceStream: el ID P6S y el XML que cuenta el
// campo de longitud. El XML termina en salto de línea y NUL, como en la
// captura. No incluye la cabecera binaria de 36 bytes.
type Command struct {
	ID      int
	Payload []byte
}

// VoiceStreamCreateRequest es el comando 148.
// En el XML el orden de atributos es Channel, VoiceMode, TransferMode,
// StreamType, que es el orden del cliente capturado.
type VoiceStreamCreateRequest struct {
	Channel      int
	StreamType   int
	TransferMode int
	VoiceMode    int
}

// VoiceStreamCreateReply es la respuesta 149.
type VoiceStreamCreateReply struct {
	MediaFlag uint32
	CmdReply  int
}

// Success es CmdReply 0, el valor de todas las respuestas de creación
// capturadas.
func (r VoiceStreamCreateReply) Success() bool { return r.CmdReply == 0 }

// VoiceStreamStartRequest es el comando 150.
type VoiceStreamStartRequest struct {
	MediaSession uint32
}

// VoiceStreamStartReply es la respuesta 151.
type VoiceStreamStartReply struct {
	Result int
}

// Success es Result 0.
func (r VoiceStreamStartReply) Success() bool { return r.Result == 0 }

// VoiceStreamStopRequest es el comando 152. Identifica la sesión con
// MediaSession, igual que Start, no con MediaFlag.
type VoiceStreamStopRequest struct {
	MediaSession uint32
}

// VoiceStreamStopReply es la respuesta 153.
type VoiceStreamStopReply struct {
	Result int
}

// Success es Result 0.
func (r VoiceStreamStopReply) Success() bool { return r.Result == 0 }

// VoiceStreamDestroyRequest es el comando 154.
type VoiceStreamDestroyRequest struct {
	MediaFlag uint32
}

// VoiceStreamDestroyReply es la respuesta 155. En la captura CmdReply vale
// 0 o 1; los dos se conservan. No hay un único código de éxito.
type VoiceStreamDestroyReply struct {
	CmdReply int
}

const xmlProlog = `<?xml version="1.0" encoding="GB2312" standalone="yes" ?>` + "\n"

func (r VoiceStreamCreateRequest) Marshal() (Command, error) {
	if err := validateChannel(r.Channel); err != nil {
		return Command{}, err
	}
	body := fmt.Sprintf(
		`<VoiceStreamCreateRequest Channel="%d" VoiceMode="%d" TransferMode="%d" StreamType="%d" />`,
		r.Channel, r.VoiceMode, r.TransferMode, r.StreamType,
	)
	return newCommand(VoiceStreamCreateRequestID, body), nil
}

func (r VoiceStreamCreateReply) Marshal() (Command, error) {
	body := fmt.Sprintf(
		`<VoiceStreamCreateReply MediaFlag="%d" CmdReply="%d" />`,
		r.MediaFlag, r.CmdReply,
	)
	return newCommand(VoiceStreamCreateReplyID, body), nil
}

func (r VoiceStreamStartRequest) Marshal() (Command, error) {
	body := fmt.Sprintf(`<VoiceStreamStartRequest MediaSession="%d" />`, r.MediaSession)
	return newCommand(VoiceStreamStartRequestID, body), nil
}

func (r VoiceStreamStartReply) Marshal() (Command, error) {
	body := fmt.Sprintf(`<VoiceStreamStartReply Result="%d" />`, r.Result)
	return newCommand(VoiceStreamStartReplyID, body), nil
}

func (r VoiceStreamStopRequest) Marshal() (Command, error) {
	body := fmt.Sprintf(`<VoiceStreamStopRequest MediaSession="%d" />`, r.MediaSession)
	return newCommand(VoiceStreamStopRequestID, body), nil
}

func (r VoiceStreamStopReply) Marshal() (Command, error) {
	body := fmt.Sprintf(`<VoiceStreamStopReply Result="%d" />`, r.Result)
	return newCommand(VoiceStreamStopReplyID, body), nil
}

func (r VoiceStreamDestroyRequest) Marshal() (Command, error) {
	body := fmt.Sprintf(`<VoiceStreamDestroyRequest MediaFlag="%d" />`, r.MediaFlag)
	return newCommand(VoiceStreamDestroyRequestID, body), nil
}

func (r VoiceStreamDestroyReply) Marshal() (Command, error) {
	body := fmt.Sprintf(`<VoiceStreamDestroyReply CmdReply="%d" />`, r.CmdReply)
	return newCommand(VoiceStreamDestroyReplyID, body), nil
}

func newCommand(id int, body string) Command {
	var b strings.Builder
	b.Grow(len(xmlProlog) + len(body) + 32)
	b.WriteString(xmlProlog)
	b.WriteString(`<Command ID="`)
	b.WriteString(strconv.Itoa(id))
	b.WriteString("\">\n    ")
	b.WriteString(body)
	b.WriteString("\n</Command>\n\x00")
	return Command{ID: id, Payload: []byte(b.String())}
}

func validateChannel(ch int) error {
	if ch < 0 {
		return fmt.Errorf("voice: %w: %d", ErrNegativeChannel, ch)
	}
	return nil
}

type commandDoc struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   []xmlChild `xml:",any"`
}

type xmlChild struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   string     `xml:",innerxml"`
}

func parseDoc(payload []byte) (commandDoc, map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(payload))
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "gb2312", "gbk", "utf-8", "us-ascii":
			return input, nil
		default:
			return nil, fmt.Errorf("voice: charset %q", charset)
		}
	}
	var doc commandDoc
	if err := dec.Decode(&doc); err != nil {
		return commandDoc{}, nil, fmt.Errorf("voice: XML: %w", err)
	}
	if doc.XMLName.Local != "Command" {
		return commandDoc{}, nil, fmt.Errorf("voice: raíz XML %q", doc.XMLName.Local)
	}
	attrs, err := attrMap(doc.Attrs)
	if err != nil {
		return commandDoc{}, nil, err
	}
	if _, err := take(attrs, "ID"); err != nil {
		return commandDoc{}, nil, err
	}
	if err := leftover(attrs); err != nil {
		return commandDoc{}, nil, err
	}
	if len(doc.Inner) != 1 {
		return commandDoc{}, nil, fmt.Errorf("voice: se esperaba un elemento VoiceStream y hay %d", len(doc.Inner))
	}
	child := doc.Inner[0]
	if strings.TrimSpace(child.Inner) != "" {
		return commandDoc{}, nil, fmt.Errorf("voice: %s no está vacío", child.XMLName.Local)
	}
	childAttrs, err := attrMap(child.Attrs)
	if err != nil {
		return commandDoc{}, nil, err
	}
	return doc, childAttrs, nil
}

func attrMap(attrs []xml.Attr) (map[string]string, error) {
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		if a.Name.Space != "" {
			return nil, fmt.Errorf("voice: atributo %s con espacio de nombres", a.Name.Local)
		}
		if _, exists := out[a.Name.Local]; exists {
			return nil, fmt.Errorf("voice: atributo duplicado %s", a.Name.Local)
		}
		out[a.Name.Local] = a.Value
	}
	return out, nil
}

func take(m map[string]string, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("voice: falta el atributo %s", key)
	}
	delete(m, key)
	return v, nil
}

func leftover(m map[string]string) error {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Errorf("voice: atributos no reconocidos: %s", strings.Join(keys, ", "))
}

func commandID(doc commandDoc) (int, error) {
	attrs, err := attrMap(doc.Attrs)
	if err != nil {
		return 0, err
	}
	raw, err := take(attrs, "ID")
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("voice: ID %q no es un entero", raw)
	}
	return id, nil
}

func requireID(doc commandDoc, want int) error {
	id, err := commandID(doc)
	if err != nil {
		return err
	}
	if id != want {
		return fmt.Errorf("voice: ID %d, se esperaba %d", id, want)
	}
	return nil
}

func parseIntAttr(m map[string]string, key string) (int, error) {
	raw, err := take(m, key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("voice: %s %q no es un entero", key, raw)
	}
	return n, nil
}

func parseResult(m map[string]string, key string) (int, error) {
	raw, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("voice: %w: falta %s", ErrResult, key)
	}
	delete(m, key)
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("voice: %w: %s %q", ErrResult, key, raw)
	}
	return n, nil
}

func parseUint32Attr(m map[string]string, key string) (uint32, error) {
	raw, err := take(m, key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("voice: %s %q no es un entero de 32 bits", key, raw)
	}
	return uint32(n), nil
}

// ParseVoiceStreamCreateRequest lee un XML 148.
func ParseVoiceStreamCreateRequest(payload []byte) (VoiceStreamCreateRequest, error) {
	doc, attrs, err := parseDoc(payload)
	if err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	if doc.Inner[0].XMLName.Local != "VoiceStreamCreateRequest" {
		return VoiceStreamCreateRequest{}, fmt.Errorf("voice: elemento %s", doc.Inner[0].XMLName.Local)
	}
	if err := requireID(doc, VoiceStreamCreateRequestID); err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	ch, err := parseIntAttr(attrs, "Channel")
	if err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	if err := validateChannel(ch); err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	voiceMode, err := parseIntAttr(attrs, "VoiceMode")
	if err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	transfer, err := parseIntAttr(attrs, "TransferMode")
	if err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	streamType, err := parseIntAttr(attrs, "StreamType")
	if err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	if err := leftover(attrs); err != nil {
		return VoiceStreamCreateRequest{}, err
	}
	return VoiceStreamCreateRequest{
		Channel:      ch,
		StreamType:   streamType,
		TransferMode: transfer,
		VoiceMode:    voiceMode,
	}, nil
}

// ParseVoiceStreamCreateReply lee un XML 149.
func ParseVoiceStreamCreateReply(payload []byte) (VoiceStreamCreateReply, error) {
	doc, attrs, err := parseDoc(payload)
	if err != nil {
		return VoiceStreamCreateReply{}, err
	}
	if doc.Inner[0].XMLName.Local != "VoiceStreamCreateReply" {
		return VoiceStreamCreateReply{}, fmt.Errorf("voice: elemento %s", doc.Inner[0].XMLName.Local)
	}
	if err := requireID(doc, VoiceStreamCreateReplyID); err != nil {
		return VoiceStreamCreateReply{}, err
	}
	flag, err := parseUint32Attr(attrs, "MediaFlag")
	if err != nil {
		return VoiceStreamCreateReply{}, err
	}
	reply, err := parseResult(attrs, "CmdReply")
	if err != nil {
		return VoiceStreamCreateReply{}, err
	}
	if err := leftover(attrs); err != nil {
		return VoiceStreamCreateReply{}, err
	}
	return VoiceStreamCreateReply{MediaFlag: flag, CmdReply: reply}, nil
}

// ParseVoiceStreamStartRequest lee un XML 150.
func ParseVoiceStreamStartRequest(payload []byte) (VoiceStreamStartRequest, error) {
	session, err := parseSession(payload, "VoiceStreamStartRequest", VoiceStreamStartRequestID)
	if err != nil {
		return VoiceStreamStartRequest{}, err
	}
	return VoiceStreamStartRequest{MediaSession: session}, nil
}

// ParseVoiceStreamStartReply lee un XML 151.
func ParseVoiceStreamStartReply(payload []byte) (VoiceStreamStartReply, error) {
	result, err := parseResultElement(payload, "VoiceStreamStartReply", VoiceStreamStartReplyID, "Result")
	if err != nil {
		return VoiceStreamStartReply{}, err
	}
	return VoiceStreamStartReply{Result: result}, nil
}

// ParseVoiceStreamStopRequest lee un XML 152.
func ParseVoiceStreamStopRequest(payload []byte) (VoiceStreamStopRequest, error) {
	session, err := parseSession(payload, "VoiceStreamStopRequest", VoiceStreamStopRequestID)
	if err != nil {
		return VoiceStreamStopRequest{}, err
	}
	return VoiceStreamStopRequest{MediaSession: session}, nil
}

// ParseVoiceStreamStopReply lee un XML 153.
func ParseVoiceStreamStopReply(payload []byte) (VoiceStreamStopReply, error) {
	result, err := parseResultElement(payload, "VoiceStreamStopReply", VoiceStreamStopReplyID, "Result")
	if err != nil {
		return VoiceStreamStopReply{}, err
	}
	return VoiceStreamStopReply{Result: result}, nil
}

// ParseVoiceStreamDestroyRequest lee un XML 154.
func ParseVoiceStreamDestroyRequest(payload []byte) (VoiceStreamDestroyRequest, error) {
	doc, attrs, err := parseDoc(payload)
	if err != nil {
		return VoiceStreamDestroyRequest{}, err
	}
	if doc.Inner[0].XMLName.Local != "VoiceStreamDestroyRequest" {
		return VoiceStreamDestroyRequest{}, fmt.Errorf("voice: elemento %s", doc.Inner[0].XMLName.Local)
	}
	if err := requireID(doc, VoiceStreamDestroyRequestID); err != nil {
		return VoiceStreamDestroyRequest{}, err
	}
	flag, err := parseUint32Attr(attrs, "MediaFlag")
	if err != nil {
		return VoiceStreamDestroyRequest{}, err
	}
	if err := leftover(attrs); err != nil {
		return VoiceStreamDestroyRequest{}, err
	}
	return VoiceStreamDestroyRequest{MediaFlag: flag}, nil
}

// ParseVoiceStreamDestroyReply lee un XML 155.
func ParseVoiceStreamDestroyReply(payload []byte) (VoiceStreamDestroyReply, error) {
	result, err := parseResultElement(payload, "VoiceStreamDestroyReply", VoiceStreamDestroyReplyID, "CmdReply")
	if err != nil {
		return VoiceStreamDestroyReply{}, err
	}
	return VoiceStreamDestroyReply{CmdReply: result}, nil
}

func parseSession(payload []byte, element string, id int) (uint32, error) {
	doc, attrs, err := parseDoc(payload)
	if err != nil {
		return 0, err
	}
	if doc.Inner[0].XMLName.Local != element {
		return 0, fmt.Errorf("voice: elemento %s", doc.Inner[0].XMLName.Local)
	}
	if err := requireID(doc, id); err != nil {
		return 0, err
	}
	session, err := parseUint32Attr(attrs, "MediaSession")
	if err != nil {
		return 0, err
	}
	if err := leftover(attrs); err != nil {
		return 0, err
	}
	return session, nil
}

func parseResultElement(payload []byte, element string, id int, attr string) (int, error) {
	doc, attrs, err := parseDoc(payload)
	if err != nil {
		return 0, err
	}
	if doc.Inner[0].XMLName.Local != element {
		return 0, fmt.Errorf("voice: elemento %s", doc.Inner[0].XMLName.Local)
	}
	if err := requireID(doc, id); err != nil {
		return 0, err
	}
	result, err := parseResult(attrs, attr)
	if err != nil {
		return 0, err
	}
	if err := leftover(attrs); err != nil {
		return 0, err
	}
	return result, nil
}
