package history

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s"
)

// Session coordina una reproducción histórica sobre conexiones ya abiertas.
// No abre conexiones de red.
type Session struct {
	commandConn net.Conn
	mediaConn   net.Conn

	mu              sync.Mutex
	commandWriteMu  sync.Mutex
	mediaMu         sync.Mutex
	sequence        uint32
	mediaFlag       uint32
	historyDataFlow atomic.Uint64
	responses       chan commandResponse
	closed          chan struct{}
	closeOnce       sync.Once
}

type commandResponse struct {
	command p6s.Command
	err     error
}

// NewSession construye una sesión sobre conexiones inyectadas. La cancelación
// del contexto de vida cierra ambas conexiones para desbloquear operaciones.
func NewSession(ctx context.Context, commandConn, mediaConn net.Conn) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("history: contexto nil")
	}
	if commandConn == nil || mediaConn == nil {
		return nil, errors.New("history: conexión nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session := &Session{
		commandConn: commandConn,
		mediaConn:   mediaConn,
		responses:   make(chan commandResponse, 1),
		closed:      make(chan struct{}),
	}
	go session.readCommands()
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = session.Close()
			case <-session.closed:
			}
		}()
	}
	return session, nil
}

// MediaFlag devuelve el identificador obtenido al crear el stream, o cero si
// Create todavía no terminó correctamente.
func (s *Session) MediaFlag() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mediaFlag
}

// Create crea el stream MultiType y enlaza su MediaFlag con la conexión media.
func (s *Session) Create(ctx context.Context, request HistoryStreamCreateMultiTypeRequest) error {
	message, err := NewHistoryStreamCreateMultiType(request)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mediaFlag != 0 {
		return errors.New("history: stream ya creado")
	}
	reply, err := s.exchangeLocked(ctx, message, IDHistoryStreamCreateReply)
	if err != nil {
		return err
	}
	if err := acceptedReply(IDHistoryStreamCreateReply, reply); err != nil {
		return err
	}
	mediaFlag, err := strconv.ParseUint(reply.MediaFlag, 10, 32)
	if err != nil || mediaFlag == 0 {
		_ = s.Close()
		return errors.New("history: MediaFlag inválido")
	}
	stopDeadline := watchConn(ctx, s.mediaConn)
	err = p6s.WriteMediaFrame(s.mediaConn, p6s.MediaLinkFrame(uint32(mediaFlag)))
	stopDeadline()
	if err != nil {
		_ = s.Close()
		return sessionContextError(ctx, fmt.Errorf("history: enlazar conexión media: %w", err))
	}
	s.mediaFlag = uint32(mediaFlag)
	return nil
}

// Start inicia o reanuda la reproducción creada.
func (s *Session) Start(ctx context.Context) error {
	return s.Control(ctx, ControlStartResume, "0")
}

// Seek mueve la reproducción al instante indicado usando la variante
// capturada Operation=2, StreamFlag=1.
func (s *Session) Seek(ctx context.Context, position DateTime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mediaFlag, err := s.requireMediaFlagLocked()
	if err != nil {
		return err
	}
	message, err := NewHistoryStreamPos(HistoryStreamPosRequest{MediaFlag: strconv.FormatUint(uint64(mediaFlag), 10), PosTime: position})
	if err != nil {
		return err
	}
	reply, err := s.exchangeLocked(ctx, message, IDHistoryStreamPosReply)
	if err != nil {
		return err
	}
	return acceptedReply(IDHistoryStreamPosReply, reply)
}

// Control envía una operación HistoryStreamCtl al stream creado.
func (s *Session) Control(ctx context.Context, operation ControlOperation, speed string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mediaFlag, err := s.requireMediaFlagLocked()
	if err != nil {
		return err
	}
	message, err := NewHistoryStreamCtl(HistoryStreamCtlRequest{
		Operation: operation,
		MediaFlag: strconv.FormatUint(uint64(mediaFlag), 10),
		Speed:     speed,
	})
	if err != nil {
		return err
	}
	reply, err := s.exchangeLocked(ctx, message, IDHistoryStreamCtlReply)
	if err != nil {
		return err
	}
	return acceptedReply(IDHistoryStreamCtlReply, reply)
}

// Stop detiene la reproducción sin destruir el stream.
func (s *Session) Stop(ctx context.Context) error {
	return s.Control(ctx, ControlPauseStop, "0")
}

// Destroy libera el stream creado. No cierra las conexiones inyectadas.
func (s *Session) Destroy(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mediaFlag, err := s.requireMediaFlagLocked()
	if err != nil {
		return err
	}
	message, err := NewHistoryStreamDestroy(HistoryStreamDestroyRequest{
		MediaFlag: strconv.FormatUint(uint64(mediaFlag), 10),
		Version:   "-1",
	})
	if err != nil {
		return err
	}
	reply, err := s.exchangeLocked(ctx, message, IDHistoryStreamDestroyReply)
	if err != nil {
		return err
	}
	if err := acceptedReply(IDHistoryStreamDestroyReply, reply); err != nil {
		return err
	}
	s.mediaFlag = 0
	return nil
}

// ReadMediaFrame lee el siguiente frame (incluido el frame de confirmación de
// enlace) de la conexión media.
func (s *Session) ReadMediaFrame(ctx context.Context) (p6s.MediaFrame, error) {
	if ctx == nil {
		return p6s.MediaFrame{}, errors.New("history: contexto nil")
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	if err := ctx.Err(); err != nil {
		return p6s.MediaFrame{}, err
	}
	stopDeadline := watchConn(ctx, s.mediaConn)
	defer stopDeadline()
	frame, err := p6s.ReadMediaFrame(s.mediaConn)
	if err != nil {
		if ctx.Err() != nil {
			_ = s.Close()
		}
		return p6s.MediaFrame{}, sessionContextError(ctx, err)
	}
	s.historyDataFlow.Add(uint64(len(frame.Payload)))
	return frame, nil
}

func (s *Session) requireMediaFlagLocked() (uint32, error) {
	if s.mediaFlag == 0 {
		return 0, errors.New("history: stream no creado")
	}
	return s.mediaFlag, nil
}

// ReplyError informa un CmdReply no exitoso sin conservar el XML de respuesta.
type ReplyError struct {
	ID   uint16
	Code string
}

func (e *ReplyError) Error() string {
	return fmt.Sprintf("history: comando %d rechazado con código %s", e.ID, e.Code)
}

func acceptedReply(id uint16, reply Reply) error {
	if reply.CmdReply != "0" {
		return &ReplyError{ID: id, Code: reply.CmdReply}
	}
	return nil
}

func (s *Session) exchangeLocked(ctx context.Context, message Message, wantID uint16) (Reply, error) {
	if ctx == nil {
		return Reply{}, errors.New("history: contexto nil")
	}
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	defer func() {
		if ctx.Err() != nil {
			_ = s.Close()
		}
	}()
	select {
	case <-s.closed:
		return Reply{}, net.ErrClosed
	default:
	}
	sequence := s.sequence
	s.sequence++
	stopDeadline := watchConn(ctx, s.commandConn)
	s.commandWriteMu.Lock()
	if err := p6s.WriteCommand(s.commandConn, p6s.Command{Sequence: sequence, XML: wirePayload(message.Payload)}); err != nil {
		s.commandWriteMu.Unlock()
		stopDeadline()
		return Reply{}, sessionContextError(ctx, err)
	}
	s.commandWriteMu.Unlock()
	stopDeadline()

	select {
	case response := <-s.responses:
		if response.err != nil {
			return Reply{}, sessionContextError(ctx, response.err)
		}
		id, _, err := commandIdentity(response.command.XML)
		if err != nil {
			return Reply{}, err
		}
		if id != wantID {
			return Reply{}, fmt.Errorf("history: ID de respuesta %d, se esperaba %d", id, wantID)
		}
		return ParseReply(Message{ID: wantID, Payload: response.command.XML})
	case <-ctx.Done():
		_ = s.Close()
		return Reply{}, ctx.Err()
	case <-s.closed:
		return Reply{}, net.ErrClosed
	}
}

func (s *Session) readCommands() {
	for {
		command, err := p6s.ReadCommand(s.commandConn)
		if err != nil {
			s.deliverResponse(commandResponse{err: err})
			return
		}
		id, name, err := commandIdentity(command.XML)
		if err != nil {
			s.deliverResponse(commandResponse{err: err})
			return
		}
		if id != 78 {
			s.deliverResponse(commandResponse{command: command})
			continue
		}
		if name != "HeartBeatNotice" {
			s.deliverResponse(commandResponse{err: errors.New("history: heartbeat inválido")})
			return
		}
		heartbeatReply := wirePayload([]byte(fmt.Sprintf(`<Command ID="79"><HeartBeatNoticeReply CmdReply="0" NetDataFlow="0" NetHistoryDataFlow="%d"></HeartBeatNoticeReply></Command>`, s.historyDataFlow.Swap(0))))
		s.commandWriteMu.Lock()
		err = p6s.WriteCommand(s.commandConn, p6s.Command{Sequence: command.Sequence, XML: heartbeatReply})
		s.commandWriteMu.Unlock()
		if err != nil {
			s.deliverResponse(commandResponse{err: err})
			return
		}
	}
}

func (s *Session) deliverResponse(response commandResponse) {
	select {
	case s.responses <- response:
	case <-s.closed:
	}
}

func commandIdentity(payload []byte) (uint16, string, error) {
	var envelope struct {
		XMLName xml.Name
		ID      uint16 `xml:"ID,attr"`
		Body    []struct {
			XMLName xml.Name
		} `xml:",any"`
	}
	if err := decodeXML(payload, &envelope); err != nil || envelope.XMLName.Local != "Command" || len(envelope.Body) != 1 {
		return 0, "", errors.New("history: sobre de comando inválido")
	}
	return envelope.ID, envelope.Body[0].XMLName.Local, nil
}

// Close cierra ambas conexiones una sola vez y desbloquea las operaciones en curso.
func (s *Session) Close() error {
	var result error
	s.closeOnce.Do(func() {
		close(s.closed)
		commandErr := s.commandConn.Close()
		mediaErr := s.mediaConn.Close()
		result = errors.Join(commandErr, mediaErr)
	})
	return result
}

func watchConn(ctx context.Context, conn net.Conn) func() {
	if ctx == nil {
		return func() {}
	}
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		_ = conn.SetDeadline(deadline)
	}
	if ctx.Done() == nil {
		return func() { _ = conn.SetDeadline(time.Time{}) }
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

func sessionContextError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
