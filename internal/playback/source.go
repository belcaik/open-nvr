package playback

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/p6s"
	"github.com/belcaik/open-nvr/internal/p6s/history"
)

func (m *Manager) SourceHandler() http.Handler {
	return http.HandlerFunc(m.serveSource)
}

func (m *Manager) serveSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.RawQuery != "" || !strings.HasPrefix(r.URL.Path, m.sourcePath+"/") {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, m.sourcePath+"/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	e := m.byToken[token]
	m.mu.Unlock()
	if e == nil {
		http.NotFound(w, r)
		return
	}
	e.mu.Lock()
	if e.sourceSet {
		e.mu.Unlock()
		http.Error(w, "Fuente ya conectada", http.StatusConflict)
		return
	}
	e.sourceSet = true
	initial := !e.started
	e.mu.Unlock()
	m.connected(e)

	var err error
	if initial {
		e.opMu.Lock()
		opctx, cancel := context.WithTimeout(e.ctx, m.opTimeout)
		confirmation, readErr := e.session.ReadMediaFrame(opctx)
		err = readErr
		if err == nil && (len(confirmation.Extension) != 4 || len(confirmation.Payload) != 0) {
			err = errors.New("playback: confirmación de enlace inesperada")
		}
		if err == nil {
			err = e.session.Stop(opctx)
		}
		if err == nil {
			err = e.session.Seek(opctx, dateTime(e.snapshot().Start))
		}
		if err == nil {
			err = e.session.Start(opctx)
		}
		if err == nil {
			err = e.session.Control(opctx, history.ControlSpeed, "0")
		}
		cancel()
		e.opMu.Unlock()
	}
	if err == nil {
		e.mu.Lock()
		e.started = true
		e.mu.Unlock()
		e.setState(StatePlaying)
	}
	if err != nil {
		http.Error(w, "No se pudo iniciar la reproducción", http.StatusBadGateway)
		_ = m.closeEntry(e)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	frames := make(chan frameResult, 1)
	sentBytes := 0
	firstPayload := true
	readCtx, readCancel := context.WithCancel(e.ctx)
	defer readCancel()
	go func() {
		for {
			frame, err := e.session.ReadMediaFrame(readCtx)
			select {
			case frames <- frameResult{frame: frame, err: err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		e.mu.Lock()
		e.sourceSet = false
		e.mu.Unlock()
		m.disconnected(e)
	}()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-e.ctx.Done():
			return
		case result := <-frames:
			if result.err != nil {
				if e.ctx.Err() == nil && r.Context().Err() == nil {
					logx.Printf("playback: lectura %s tras %d bytes: %v", e.view.ID, sentBytes, result.err)
				}
				return
			}
			if len(result.frame.Payload) == 0 {
				continue
			}
			payload := result.frame.Payload
			if firstPayload {
				var ok bool
				payload, ok = hevcBitstream(payload)
				if !ok {
					logx.Printf("playback: %s no contiene un VPS HEVC inicial", e.view.ID)
					return
				}
				firstPayload = false
			}
			if _, err := w.Write(payload); err != nil {
				return
			}
			sentBytes += len(payload)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func hevcBitstream(payload []byte) ([]byte, bool) {
	if !bytes.HasPrefix(payload, []byte{0, 0, 1, 0xb7}) {
		return payload, true
	}
	if i := bytes.Index(payload, []byte{0, 0, 0, 1, 0x40}); i > 0 {
		return payload[i:], true
	}
	if i := bytes.Index(payload, []byte{0, 0, 1, 0x40}); i > 0 {
		return payload[i:], true
	}
	return nil, false
}

type frameResult struct {
	frame p6s.MediaFrame
	err   error
}
