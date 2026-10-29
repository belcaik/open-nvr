package voice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s"
)

// ErrCmdReply indica que VoiceStreamCreateReply no trajo CmdReply 0.
var ErrCmdReply = errors.New("CmdReply distinto de 0")

const (
	heartBeatNoticeID      = 78
	heartBeatNoticeReplyID = 79
)

// Config describe una sesión de voz corta sobre conexiones ya abiertas.
// Channel es base 0. VoiceMode, TransferMode y StreamType por defecto son
// los de la captura (2, 1, 0). No hay Dial.
type Config struct {
	Channel        int
	VoiceMode      int
	TransferMode   int
	StreamType     int
	Extension      AudioExtension
	Sequence       uint32
	CommandTimeout time.Duration
	FrameInterval  time.Duration
	Sleep          func(context.Context, time.Duration) error
}

// Session es una sesión VoiceStream sobre command (6060) y media (6066).
type Session struct {
	command  net.Conn
	media    net.Conn
	cfg      Config
	flag     uint32
	seq      uint32
	lastSend time.Time

	cmdMu     sync.Mutex
	replies   chan p6s.Command
	cmdDone   chan struct{}
	mediaDone chan struct{}
	errMu     sync.Mutex
	ioErr     error

	sendMu sync.Mutex
	mu     sync.Mutex
	closed bool
}

// Open envía VoiceStreamCreate (148), la trama de enlace, VoiceStreamStart
// (150) y valida CmdReply/Result. Las conexiones las aporta el llamador; Open
// no hace Dial. Si falla después de obtener MediaFlag, envía Stop/Destroy y
// cierra ambas conexiones.
func Open(ctx context.Context, command, media net.Conn, cfg Config) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("voice: contexto nulo")
	}
	if command == nil || media == nil {
		return nil, errors.New("voice: conexión nula")
	}
	if err := ctx.Err(); err != nil {
		command.Close()
		media.Close()
		return nil, err
	}
	cfg = cfg.withDefaults()
	s := &Session{command: command, media: media, cfg: cfg, seq: cfg.Sequence}
	s.startIO()
	waitCtx, cancel := context.WithTimeout(ctx, cfg.CommandTimeout)
	defer cancel()
	stopCmd := watchContext(waitCtx, command)
	stopMedia := watchContext(waitCtx, media)
	defer stopCmd()
	defer stopMedia()

	create, err := (VoiceStreamCreateRequest{
		Channel:      cfg.Channel,
		VoiceMode:    cfg.VoiceMode,
		TransferMode: cfg.TransferMode,
		StreamType:   cfg.StreamType,
	}).Marshal()
	if err != nil {
		s.closeConns()
		return nil, err
	}
	if err := s.writeCommand(s.seq, create.Payload); err != nil {
		s.closeConns()
		return nil, contextError(waitCtx, err)
	}
	got, err := s.waitCommand(waitCtx)
	if err != nil {
		s.closeConns()
		return nil, err
	}
	reply, err := ParseVoiceStreamCreateReply(got.XML)
	if err != nil {
		s.closeConns()
		return nil, err
	}
	if !reply.Success() {
		s.closeConns()
		return nil, fmt.Errorf("voice: %w: CmdReply=%d", ErrCmdReply, reply.CmdReply)
	}
	s.flag = reply.MediaFlag
	s.seq++

	if err := p6s.WriteMediaFrame(media, p6s.MediaLinkFrame(s.flag)); err != nil {
		return nil, s.fail(waitCtx, err)
	}
	start, err := (VoiceStreamStartRequest{MediaSession: s.flag}).Marshal()
	if err != nil {
		return nil, s.fail(waitCtx, err)
	}
	if err := s.writeCommand(s.seq, start.Payload); err != nil {
		s.seq++
		return nil, s.fail(waitCtx, err)
	}
	s.seq++
	gotStart, err := s.waitCommand(waitCtx)
	if err != nil {
		return nil, s.fail(waitCtx, err)
	}
	startReply, err := ParseVoiceStreamStartReply(gotStart.XML)
	if err != nil {
		return nil, s.fail(waitCtx, err)
	}
	if !startReply.Success() {
		return nil, s.fail(waitCtx, fmt.Errorf("voice: %w: Result=%d", ErrResult, startReply.Result))
	}
	return s, nil
}

func (s *Session) fail(ctx context.Context, err error) error {
	return errors.Join(contextError(ctx, err), s.Close())
}

// MediaFlag es el identificador de la sesión en comandos y en la trama de enlace.
func (s *Session) MediaFlag() uint32 { return s.flag }

// Send escribe un bloque PCMU de 320 bytes con la extensión de 44 bytes y
// espera el intervalo de 40 ms respecto del bloque anterior.
func (s *Session) Send(ctx context.Context, pcmu []byte) error {
	if ctx == nil {
		return errors.New("voice: contexto nulo")
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("voice: sesión cerrada")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidatePCMUPayload(pcmu); err != nil {
		return err
	}
	if !s.lastSend.IsZero() {
		if err := s.sleep(ctx, s.cfg.FrameInterval); err != nil {
			return err
		}
	}
	stopCmd := watchContext(ctx, s.command)
	stopMedia := watchContext(ctx, s.media)
	defer stopCmd()
	defer stopMedia()
	frame := p6s.MediaFrame{
		Extension: s.cfg.Extension.Bytes(),
		Payload:   append([]byte(nil), pcmu...),
	}
	if err := p6s.WriteMediaFrame(s.media, frame); err != nil {
		return contextError(ctx, err)
	}
	s.lastSend = time.Now()
	return nil
}

func (s *Session) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if s.cfg.Sleep != nil {
		return s.cfg.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c Config) withDefaults() Config {
	if c.VoiceMode == 0 {
		c.VoiceMode = 2
	}
	if c.TransferMode == 0 {
		c.TransferMode = 1
	}
	if c.FrameInterval == 0 {
		c.FrameInterval = FrameDuration
	}
	if c.CommandTimeout == 0 {
		c.CommandTimeout = 5 * time.Second
	}
	return c
}

// Close envía Stop (152) con MediaSession y Destroy (154) con MediaFlag, y
// cierra las dos conexiones aunque alguno de los dos falle.
func (s *Session) Close() error {
	s.sendMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.sendMu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.sendMu.Unlock()

	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.CommandTimeout)
	defer cancel()
	stopCmd := watchContext(ctx, s.command)
	defer stopCmd()
	if s.flag != 0 {
		if err := s.sendStop(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := s.sendDestroy(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	s.closeConns()
	return errors.Join(errs...)
}

func (s *Session) sendStop(ctx context.Context) error {
	stop, err := (VoiceStreamStopRequest{MediaSession: s.flag}).Marshal()
	if err != nil {
		return err
	}
	if err := s.writeCommand(s.seq, stop.Payload); err != nil {
		return contextError(ctx, err)
	}
	s.seq++
	got, err := s.waitCommand(ctx)
	if err != nil {
		return err
	}
	reply, err := ParseVoiceStreamStopReply(got.XML)
	if err != nil {
		return err
	}
	if !reply.Success() {
		return fmt.Errorf("voice: %w: Result=%d", ErrResult, reply.Result)
	}
	return nil
}

func (s *Session) sendDestroy(ctx context.Context) error {
	destroy, err := (VoiceStreamDestroyRequest{MediaFlag: s.flag}).Marshal()
	if err != nil {
		return err
	}
	if err := s.writeCommand(s.seq, destroy.Payload); err != nil {
		return contextError(ctx, err)
	}
	s.seq++
	got, err := s.waitCommand(ctx)
	if err != nil {
		return err
	}
	_, err = ParseVoiceStreamDestroyReply(got.XML)
	return err
}

func (s *Session) startIO() {
	s.replies = make(chan p6s.Command, 8)
	s.cmdDone = make(chan struct{})
	s.mediaDone = make(chan struct{})
	go s.readCommands()
	go s.drainMedia()
}

func (s *Session) readCommands() {
	defer close(s.cmdDone)
	for {
		got, err := p6s.ReadCommand(s.command)
		if err != nil {
			s.setIOErr(err)
			return
		}
		doc, _, err := parseDoc(got.XML)
		if err != nil {
			s.setIOErr(err)
			return
		}
		id, err := commandID(doc)
		if err != nil {
			s.setIOErr(err)
			return
		}
		if id == heartBeatNoticeID {
			if doc.Inner[0].XMLName.Local != "HeartBeatNotice" {
				s.setIOErr(fmt.Errorf("voice: latido con elemento %s", doc.Inner[0].XMLName.Local))
				return
			}
			if err := s.replyHeartbeat(got.Sequence); err != nil {
				s.setIOErr(err)
				return
			}
			continue
		}
		select {
		case s.replies <- got:
		default:
			s.setIOErr(fmt.Errorf("voice: comando inesperado ID %d", id))
			return
		}
	}
}

func (s *Session) drainMedia() {
	defer close(s.mediaDone)
	for {
		if _, err := p6s.ReadMediaFrame(s.media); err != nil {
			return
		}
	}
}

func (s *Session) waitCommand(ctx context.Context) (p6s.Command, error) {
	for {
		select {
		case <-ctx.Done():
			return p6s.Command{}, ctx.Err()
		case got := <-s.replies:
			return got, nil
		case <-s.cmdDone:
			select {
			case got := <-s.replies:
				return got, nil
			default:
			}
			if err := ctx.Err(); err != nil {
				return p6s.Command{}, err
			}
			if err := s.readerError(); err != nil {
				return p6s.Command{}, err
			}
			return p6s.Command{}, net.ErrClosed
		}
	}
}

func (s *Session) writeCommand(sequence uint32, xml []byte) error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	return p6s.WriteCommand(s.command, p6s.Command{Sequence: sequence, XML: xml})
}

func (s *Session) replyHeartbeat(sequence uint32) error {
	reply := newCommand(heartBeatNoticeReplyID, `<HeartBeatNoticeReply CmdReply="0" NetDataFlow="0" NetHistoryDataFlow="0" />`)
	return s.writeCommand(sequence, reply.Payload)
}

func (s *Session) setIOErr(err error) {
	s.errMu.Lock()
	if s.ioErr == nil {
		s.ioErr = err
	}
	s.errMu.Unlock()
}

func (s *Session) readerError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.ioErr
}

func (s *Session) closeConns() {
	if s.command != nil {
		_ = s.command.Close()
	}
	if s.media != nil {
		_ = s.media.Close()
	}
	if s.cmdDone != nil {
		<-s.cmdDone
	}
	if s.mediaDone != nil {
		<-s.mediaDone
	}
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

// Run abre una sesión corta, envía los bloques PCMU y cierra con Stop/Destroy.
func Run(ctx context.Context, command, media net.Conn, cfg Config, frames [][]byte) (err error) {
	session, err := Open(ctx, command, media, cfg)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, session.Close())
	}()
	for _, frame := range frames {
		if err := session.Send(ctx, frame); err != nil {
			return err
		}
	}
	return nil
}
