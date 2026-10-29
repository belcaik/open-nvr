// Package p6s implements the binary framing and login response used by the
// private P6S command and media protocols.
package p6s

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidRandom reports a login random value outside the range accepted
	// by the P6S peer.
	ErrInvalidRandom = errors.New("p6s: random must be between 1 and 32767")

	// ErrInvalidLoginXML reports a malformed login response. The underlying XML
	// error is deliberately not exposed because response XML can be sensitive.
	ErrInvalidLoginXML = errors.New("p6s: invalid login response XML")

	// ErrUnexpectedCommand reports a response that cannot be correlated with
	// the login request currently in flight.
	ErrUnexpectedCommand = errors.New("p6s: unexpected command")

	// ErrLoginRejected reports a nonzero CmdReply in UserLoginReply.
	ErrLoginRejected = errors.New("p6s: login rejected")
)

const xmlProlog = `<?xml version="1.0" encoding="GB2312" standalone="yes" ?>`

var namelessVersionCapability = regexp.MustCompile(`<[\t ]+Major="[^"]*"[\t ]+Minor="[^"]*"[\t ]+Revision="[^"]*"[\t ]*/>`)

// Params contains the client identity and challenge-response inputs. Random
// must be injected by the caller so Login remains deterministic in tests.
type Params struct {
	Username []byte
	Password []byte
	UserIP   string
	UserMAC  string
	Random   uint32
}

// LoginResult is the successful UserLoginReply. CapabilitiesXML contains only
// capability child elements, never the authentication reply element or the
// credentials sent by the client.
type LoginResult struct {
	TCPPort         uint16
	RTPPort         uint16
	CmdPort         uint16
	LoginFlagClient string
	CapabilitiesXML []byte
}

// UnexpectedCommandError identifies an uncorrelated command without retaining
// or disclosing its XML body.
type UnexpectedCommandError struct {
	Got  uint32
	Want uint32
}

func (e *UnexpectedCommandError) Error() string {
	return fmt.Sprintf("p6s: unexpected command ID %d while waiting for ID %d", e.Got, e.Want)
}

func (e *UnexpectedCommandError) Unwrap() error { return ErrUnexpectedCommand }

// LoginReplyError reports the numeric CmdReply without retaining response XML.
type LoginReplyError struct{ Code int }

func (e *LoginReplyError) Error() string {
	return fmt.Sprintf("p6s: login rejected with code %d", e.Code)
}
func (e *LoginReplyError) Unwrap() error { return ErrLoginRejected }

// Login performs the P6S challenge-response exchange on an already-connected
// command channel. It never opens a network connection.
func Login(ctx context.Context, conn net.Conn, params Params) (LoginResult, error) {
	if ctx == nil {
		return LoginResult{}, errors.New("p6s: nil context")
	}
	if conn == nil {
		return LoginResult{}, errors.New("p6s: nil connection")
	}
	if err := ctx.Err(); err != nil {
		return LoginResult{}, err
	}
	if _, err := LoginFlag(params.Password, 0, params.Random); err != nil {
		return LoginResult{}, err
	}
	stopDeadline := watchContext(ctx, conn)
	defer stopDeadline()

	if err := writeLoginCommand(conn, 0, 26, "LoginGetFlag", nil); err != nil {
		return LoginResult{}, contextError(ctx, err)
	}
	flagReply, err := waitForLoginReply(ctx, conn, 27, "LoginGetFlagReply")
	if err != nil {
		return LoginResult{}, err
	}
	challenge, err := requiredUint32Attr(flagReply, "LoginFlag")
	if err != nil {
		return LoginResult{}, err
	}
	flag, err := LoginFlag(params.Password, challenge, params.Random)
	if err != nil {
		return LoginResult{}, err
	}

	attrs := []xml.Attr{
		{Name: xml.Name{Local: "UserName"}, Value: string(params.Username)},
		{Name: xml.Name{Local: "UserIP"}, Value: params.UserIP},
		{Name: xml.Name{Local: "UserMAC"}, Value: params.UserMAC},
		{Name: xml.Name{Local: "LoginFlag"}, Value: flag},
	}
	if err := writeLoginCommand(conn, 0, 24, "UserLogin", attrs); err != nil {
		return LoginResult{}, contextError(ctx, err)
	}
	reply, siblings, err := waitForLoginReplyWithSiblings(ctx, conn, 25, "UserLoginReply")
	if err != nil {
		return LoginResult{}, err
	}
	cmdReply, err := requiredIntAttr(reply, "CmdReply")
	if err != nil {
		return LoginResult{}, err
	}
	if cmdReply != 0 {
		return LoginResult{}, &LoginReplyError{Code: cmdReply}
	}

	tcpPort, err := requiredPortAttr(reply, "TCPPort")
	if err != nil {
		return LoginResult{}, err
	}
	rtpPort, err := requiredPortAttr(reply, "RTPPort")
	if err != nil {
		return LoginResult{}, err
	}
	cmdPort, err := requiredPortAttr(reply, "CmdPort")
	if err != nil {
		return LoginResult{}, err
	}
	clientFlagValue, err := requiredUint32Attr(reply, "LoginFlagClient")
	if err != nil || clientFlagValue != params.Random+1 {
		return LoginResult{}, ErrInvalidLoginXML
	}
	clientFlag, _ := attr(reply.Attr, "LoginFlagClient")
	capabilities, err := capabilityXML(reply.InnerXML, siblings)
	if err != nil {
		return LoginResult{}, ErrInvalidLoginXML
	}
	return LoginResult{
		TCPPort:         tcpPort,
		RTPPort:         rtpPort,
		CmdPort:         cmdPort,
		LoginFlagClient: clientFlag,
		CapabilitiesXML: capabilities,
	}, nil
}

// LoginFlag calculates the hexadecimal challenge response sent by UserLogin.
// The password is copied before it is padded or truncated.
func LoginFlag(password []byte, challenge, random uint32) (string, error) {
	if random < 1 || random > 32767 {
		return "", ErrInvalidRandom
	}
	plain := []byte(fmt.Sprintf("%8x%8x", challenge+0xb1, random))
	key := make([]byte, 16)
	copy(key, password)

	encrypted := make([]byte, 0, len(plain))
	for offset := 0; offset < len(plain); offset += 8 {
		block := plain[offset : offset+8]
		if len(password) > 8 {
			block = cryptLoginDES(block, key[:8], true)
			block = cryptLoginDES(block, key[8:], false)
			block = cryptLoginDES(block, key[:8], true)
		} else {
			block = cryptLoginDES(block, key[:8], true)
		}
		encrypted = append(encrypted, block...)
	}
	for index, value := range encrypted {
		encrypted[index] = value&0x55<<1 | value&0xaa>>1
	}
	return hex.EncodeToString(encrypted), nil
}

type loginEnvelope struct {
	XMLName  xml.Name     `xml:"Command"`
	ID       uint32       `xml:"ID,attr"`
	Children []loginChild `xml:",any"`
}

type loginChild struct {
	XMLName  xml.Name
	Attr     []xml.Attr `xml:",any,attr"`
	InnerXML string     `xml:",innerxml"`
}

func writeLoginCommand(conn net.Conn, sequence, id uint32, name string, attrs []xml.Attr) error {
	var body bytes.Buffer
	body.WriteString(xmlProlog)
	encoder := xml.NewEncoder(&body)
	root := xml.StartElement{Name: xml.Name{Local: "Command"}, Attr: []xml.Attr{{Name: xml.Name{Local: "ID"}, Value: strconv.FormatUint(uint64(id), 10)}}}
	child := xml.StartElement{Name: xml.Name{Local: name}, Attr: attrs}
	if err := encoder.EncodeToken(root); err != nil {
		return errors.New("p6s: encode login command")
	}
	if err := encoder.EncodeToken(child); err != nil {
		return errors.New("p6s: encode login command")
	}
	if err := encoder.EncodeToken(child.End()); err != nil {
		return errors.New("p6s: encode login command")
	}
	if err := encoder.EncodeToken(root.End()); err != nil {
		return errors.New("p6s: encode login command")
	}
	if err := encoder.Flush(); err != nil {
		return errors.New("p6s: encode login command")
	}
	body.WriteByte(0)
	return WriteCommand(conn, Command{Sequence: sequence, XML: body.Bytes()})
}

func waitForLoginReply(ctx context.Context, conn net.Conn, wantID uint32, wantName string) (loginChild, error) {
	reply, _, err := waitForLoginReplyWithSiblings(ctx, conn, wantID, wantName)
	return reply, err
}

func waitForLoginReplyWithSiblings(ctx context.Context, conn net.Conn, wantID uint32, wantName string) (loginChild, []loginChild, error) {
	for {
		command, err := ReadCommand(conn)
		if err != nil {
			return loginChild{}, nil, contextError(ctx, err)
		}
		envelope, err := parseLoginEnvelope(command.XML)
		if err != nil {
			return loginChild{}, nil, err
		}
		if envelope.ID == 78 {
			if len(envelope.Children) != 1 || envelope.Children[0].XMLName.Local != "HeartBeatNotice" {
				return loginChild{}, nil, ErrInvalidLoginXML
			}
			attrs := []xml.Attr{
				{Name: xml.Name{Local: "CmdReply"}, Value: "0"},
				{Name: xml.Name{Local: "NetDataFlow"}, Value: "0"},
				{Name: xml.Name{Local: "NetHistoryDataFlow"}, Value: "0"},
			}
			if err := writeLoginCommand(conn, command.Sequence, 79, "HeartBeatNoticeReply", attrs); err != nil {
				return loginChild{}, nil, contextError(ctx, err)
			}
			continue
		}
		if envelope.ID != wantID {
			return loginChild{}, nil, &UnexpectedCommandError{Got: envelope.ID, Want: wantID}
		}
		matched := -1
		for index, child := range envelope.Children {
			if child.XMLName.Local == wantName {
				if matched >= 0 {
					return loginChild{}, nil, ErrInvalidLoginXML
				}
				matched = index
			}
		}
		if matched < 0 {
			return loginChild{}, nil, ErrInvalidLoginXML
		}
		siblings := make([]loginChild, 0, len(envelope.Children)-1)
		siblings = append(siblings, envelope.Children[:matched]...)
		siblings = append(siblings, envelope.Children[matched+1:]...)
		return envelope.Children[matched], siblings, nil
	}
}

func parseLoginEnvelope(data []byte) (loginEnvelope, error) {
	data = bytes.TrimRight(data, "\x00")
	data = namelessVersionCapability.ReplaceAll(data, nil)
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	var envelope loginEnvelope
	if err := decoder.Decode(&envelope); err != nil || envelope.XMLName.Local != "Command" {
		return loginEnvelope{}, ErrInvalidLoginXML
	}
	return envelope, nil
}

func requiredUint32Attr(child loginChild, name string) (uint32, error) {
	value, ok := attr(child.Attr, name)
	if !ok {
		return 0, ErrInvalidLoginXML
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err == nil {
		return uint32(parsed), nil
	}
	signed, signedErr := strconv.ParseInt(value, 10, 32)
	if signedErr != nil {
		return 0, ErrInvalidLoginXML
	}
	return uint32(int32(signed)), nil
}

func requiredIntAttr(child loginChild, name string) (int, error) {
	value, ok := attr(child.Attr, name)
	if !ok {
		return 0, ErrInvalidLoginXML
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, ErrInvalidLoginXML
	}
	return parsed, nil
}

func requiredPortAttr(child loginChild, name string) (uint16, error) {
	value, ok := attr(child.Attr, name)
	if !ok {
		return 0, ErrInvalidLoginXML
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, ErrInvalidLoginXML
	}
	return uint16(parsed), nil
}

func attr(attrs []xml.Attr, name string) (string, bool) {
	for _, candidate := range attrs {
		if candidate.Name.Local == name {
			return candidate.Value, true
		}
	}
	return "", false
}

func capabilityXML(inner string, siblings []loginChild) ([]byte, error) {
	var result bytes.Buffer
	result.WriteString(strings.TrimSpace(inner))
	encoder := xml.NewEncoder(&result)
	for _, sibling := range siblings {
		if err := encoder.Encode(sibling); err != nil {
			return nil, err
		}
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(result.Bytes()), nil
}

func watchContext(ctx context.Context, conn net.Conn) func() {
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline && ctx.Done() == nil {
		return func() {}
	}
	if hasDeadline {
		if err := conn.SetDeadline(deadline); err != nil {
			return func() {}
		}
	} else if err := conn.SetDeadline(time.Time{}); err != nil {
		return func() {}
	}

	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stopped:
		}
	}()
	return func() {
		close(stopped)
		<-done
		_ = conn.SetDeadline(time.Time{})
	}
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
