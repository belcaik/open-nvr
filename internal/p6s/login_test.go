package p6s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestLoginFlagVectors(t *testing.T) {
	tests := []struct {
		name      string
		password  string
		challenge uint32
		random    uint32
		want      string
	}{
		{"des", "password", 0, 0x1234, "edcb9a37dd8b642567bb2a23bb3bd15d"},
		{"two-key 3des", "password1234", 0, 0x1234, "5a279a309cc6cdf8c8fc822ef97b7623"},
		{"s-box and output bit order", "example-pass-123", 0x12345678, 0x1234, "66abe94e99ebf736e0e8872beb021d34"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := LoginFlag([]byte(test.password), test.challenge, test.random)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("LoginFlag() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLoginFlagRejectsRandomOutsideRange(t *testing.T) {
	for _, random := range []uint32{0, 32768, ^uint32(0)} {
		if _, err := LoginFlag([]byte("do-not-log-me"), 1, random); !errors.Is(err, ErrInvalidRandom) {
			t.Fatalf("random %d: error = %v, want ErrInvalidRandom", random, err)
		}
	}
}

func TestLoginFragmentedWithHeartbeatAndBothFlagVectors(t *testing.T) {
	tests := []struct {
		name      string
		password  string
		challenge uint32
		random    uint32
		wantFlag  string
	}{
		{"des", "password", 0, 0x1234, "edcb9a37dd8b642567bb2a23bb3bd15d"},
		{"two-key 3des", "password1234", 0, 0x1234, "5a279a309cc6cdf8c8fc822ef97b7623"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			serverErr := make(chan error, 1)
			go func() {
				serverErr <- serveSuccessfulLogin(server, test.challenge, test.wantFlag, test.random+1)
			}()

			result, err := Login(context.Background(), client, Params{
				Username: []byte(`alice<&"`),
				Password: []byte(test.password),
				UserIP:   "192.0.2.10",
				UserMAC:  "00:11:22:33:44:55",
				Random:   test.random,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-serverErr; err != nil {
				t.Fatal(err)
			}
			if result.TCPPort != 6066 || result.RTPPort != 554 || result.CmdPort != 6060 || result.LoginFlagClient != "4661" {
				t.Fatalf("unexpected result: %+v", result)
			}
			if got := string(result.CapabilitiesXML); !strings.Contains(got, `<Ability Name="Voice"`) {
				t.Fatalf("capabilities = %q", got)
			}
			if bytes.Contains(result.CapabilitiesXML, []byte("UserLoginReply")) || bytes.Contains(result.CapabilitiesXML, []byte("LoginFlagClient")) {
				t.Fatalf("capabilities retain login data: %q", result.CapabilitiesXML)
			}
		})
	}
}

func TestLoginReturnsCmdReplyError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="27"><LoginGetFlagReply LoginFlag="1"/></Command>`)
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="25"><UserLoginReply CmdReply="7"/></Command>`)
	}()

	_, err := Login(context.Background(), client, Params{Password: []byte("secret"), Random: 1})
	var replyErr *LoginReplyError
	if !errors.As(err, &replyErr) || replyErr.Code != 7 || !errors.Is(err, ErrLoginRejected) {
		t.Fatalf("error = %v, want LoginReplyError code 7", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "LoginFlag") {
		t.Fatalf("error leaks authentication material: %v", err)
	}
}

func TestLoginIgnoresNamelessVersionCapability(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="27"><LoginGetFlagReply LoginFlag="1"/></Command>`)
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="25"><UserLoginReply CmdReply="0" TCPPort="6066" RTPPort="554" CmdPort="6060" LoginFlagClient="2">
<HistoryChannelMultiType Major="1" Minor="2" Revision="3"/>
< Major="-1" Minor="-1" Revision="-1" />
<SetChromaDefault Major="4" Minor="5" Revision="6"/>
</UserLoginReply></Command>`)
	}()

	result, err := Login(context.Background(), client, Params{Password: []byte("secret"), Random: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.CapabilitiesXML, []byte("HistoryChannelMultiType")) || !bytes.Contains(result.CapabilitiesXML, []byte("SetChromaDefault")) {
		t.Fatalf("valid capabilities were not retained: %q", result.CapabilitiesXML)
	}
}

func TestLoginRejectsMismatchedClientFlag(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="27"><LoginGetFlagReply LoginFlag="1"/></Command>`)
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="25"><UserLoginReply CmdReply="0" TCPPort="6066" RTPPort="554" CmdPort="6060" LoginFlagClient="3"/></Command>`)
	}()

	_, err := Login(context.Background(), client, Params{Password: []byte("secret"), Random: 1})
	if !errors.Is(err, ErrInvalidLoginXML) {
		t.Fatalf("error = %v, want ErrInvalidLoginXML", err)
	}
}

func TestLoginRejectsUnexpectedID(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		_, _ = ReadCommand(server)
		_ = writeTestCommand(server, 0, `<Command ID="137"><RealStreamCreateReply Result="0"/></Command>`)
	}()

	_, err := Login(context.Background(), client, Params{Password: []byte("secret"), Random: 1})
	var unexpected *UnexpectedCommandError
	if !errors.As(err, &unexpected) || unexpected.Got != 137 || unexpected.Want != 27 {
		t.Fatalf("error = %v, want unexpected ID 137", err)
	}
}

func TestLoginCancellationInterruptsIOAndClearsDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	requestRead := make(chan struct{})
	go func() {
		_, _ = ReadCommand(server)
		close(requestRead)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Login(ctx, client, Params{Password: []byte("secret"), Random: 1})
		done <- err
	}()
	<-requestRead
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Login did not stop after cancellation")
	}

	read := make(chan byte, 1)
	go func() {
		var data [1]byte
		_, _ = server.Read(data[:])
		read <- data[0]
	}()
	if _, err := client.Write([]byte{'x'}); err != nil {
		t.Fatalf("deadline was not cleared: %v", err)
	}
	if got := <-read; got != 'x' {
		t.Fatalf("post-cancel byte = %q", got)
	}
}

func serveSuccessfulLogin(conn net.Conn, challenge uint32, wantFlag string, clientFlag uint32) error {
	request, err := ReadCommand(conn)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(request.XML, []byte(xmlProlog)) || request.XML[len(request.XML)-1] != 0 {
		return errors.New("LoginGetFlag lacks GB2312 prolog or final NUL")
	}
	envelope, err := parseLoginEnvelope(request.XML)
	if err != nil || envelope.ID != 26 || envelope.Children[0].XMLName.Local != "LoginGetFlag" {
		return fmt.Errorf("unexpected LoginGetFlag: %w", err)
	}
	if err := writeTestCommandFragmented(conn, 0x10203040, `<Command ID="78"><HeartBeatNotice/></Command>`); err != nil {
		return err
	}
	heartbeat, err := ReadCommand(conn)
	if err != nil {
		return err
	}
	heartbeatEnvelope, err := parseLoginEnvelope(heartbeat.XML)
	if err != nil || heartbeat.Sequence != 0x10203040 || heartbeatEnvelope.ID != 79 {
		return errors.New("invalid heartbeat reply")
	}
	heartbeatReply := heartbeatEnvelope.Children[0]
	for _, name := range []string{"CmdReply", "NetDataFlow", "NetHistoryDataFlow"} {
		if value, ok := attr(heartbeatReply.Attr, name); !ok || value != "0" {
			return fmt.Errorf("heartbeat %s = %q", name, value)
		}
	}
	if err := writeTestCommandFragmented(conn, 0, fmt.Sprintf(`<Command ID="27"><LoginGetFlagReply LoginFlag="%d" Ret="-1"/></Command>`, challenge)); err != nil {
		return err
	}

	request, err = ReadCommand(conn)
	if err != nil {
		return err
	}
	if bytes.Contains(request.XML, []byte("password")) || bytes.Contains(request.XML, []byte("12345")) {
		return errors.New("UserLogin contains a password")
	}
	envelope, err = parseLoginEnvelope(request.XML)
	if err != nil || envelope.ID != 24 || envelope.Children[0].XMLName.Local != "UserLogin" {
		return errors.New("invalid UserLogin")
	}
	loginRequest := envelope.Children[0]
	if value, _ := attr(loginRequest.Attr, "LoginFlag"); value != wantFlag {
		return fmt.Errorf("LoginFlag = %q, want %q", value, wantFlag)
	}
	if value, _ := attr(loginRequest.Attr, "UserName"); value != `alice<&"` {
		return fmt.Errorf("UserName was not XML escaped safely: %q", value)
	}
	return writeTestCommandFragmented(conn, 0, fmt.Sprintf(`<Command ID="25"><UserLoginReply CmdReply="0" TCPPort="6066" RTPPort="554" CmdPort="6060" LoginFlagClient="%d"><Ability Name="Voice" Enabled="1"/></UserLoginReply></Command>`, clientFlag))
}

func writeTestCommand(conn net.Conn, sequence uint32, body string) error {
	xmlBody := append([]byte(xmlProlog+body), 0)
	return WriteCommand(conn, Command{Sequence: sequence, XML: xmlBody})
}

func writeTestCommandFragmented(conn net.Conn, sequence uint32, body string) error {
	var encoded bytes.Buffer
	if err := WriteCommand(&encoded, Command{Sequence: sequence, XML: append([]byte(xmlProlog+body), 0)}); err != nil {
		return err
	}
	for _, value := range encoded.Bytes() {
		if _, err := conn.Write([]byte{value}); err != nil {
			return err
		}
	}
	return nil
}
