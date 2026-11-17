package modbus

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ErrServerClosed is returned by Serve after Close or Shutdown.
var ErrServerClosed = errors.New("modbus: server closed")

// Config holds server tuning parameters. Zero values select defaults.
type Config struct {
	// MaxClients limits concurrent connections; extra connections are
	// accepted and immediately closed. Default 32.
	MaxClients int
	// IdleTimeout closes a connection that sends no request for this long.
	// Zero disables the timeout.
	IdleTimeout time.Duration
	// WriteTimeout bounds writing one response. Default 5s.
	WriteTimeout time.Duration
	// Logger receives structured logs. Default: discard.
	Logger *slog.Logger
}

// Server is a Modbus TCP server. Create it with NewServer, run it with Serve
// and stop it with Shutdown (graceful) or Close (immediate).
type Server struct {
	cfg     Config
	handler Handler
	log     *slog.Logger

	closing atomic.Bool
	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

// NewServer returns a server that serves requests from h.
func NewServer(cfg Config, h Handler) *Server {
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = 32
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{cfg: cfg, handler: h, log: log, conns: make(map[net.Conn]struct{})}
}

// Serve accepts connections on ln and serves each in its own goroutine. It
// blocks until the server is closed, then returns ErrServerClosed. Serve takes
// ownership of ln and may be called at most once.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.ln != nil {
		s.mu.Unlock()
		return errors.New("modbus: Serve called more than once")
	}
	s.ln = ln
	closing := s.closing.Load()
	s.mu.Unlock()
	if closing {
		ln.Close()
		return ErrServerClosed
	}

	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closing.Load() {
				return ErrServerClosed
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Transient errors (e.g. EMFILE): back off like net/http does.
			backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
			s.log.Warn("accept failed, retrying", "err", err, "backoff", backoff)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		if ok, reason := s.track(conn); !ok {
			s.log.Warn("connection rejected", "remote", conn.RemoteAddr().String(), "reason", reason)
			conn.Close()
			continue
		}
		go s.serveConn(conn)
	}
}

// track registers conn; wg.Add happens under mu so it can never race with
// the wg.Wait in Shutdown (closing is set under mu before Wait).
func (s *Server) track(c net.Conn) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() {
		return false, "server shutting down"
	}
	if len(s.conns) >= s.cfg.MaxClients {
		return false, "max clients reached"
	}
	s.conns[c] = struct{}{}
	s.wg.Add(1)
	return true, ""
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	c.Close()
	s.wg.Done()
}

// ClientCount returns the number of currently connected clients.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// beginClose stops accepting and wakes idle connections. A connection that is
// in the middle of a request finishes it (including the response) and then
// exits at the top of its loop.
func (s *Server) beginClose() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Swap(true) {
		return nil
	}
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	for c := range s.conns {
		_ = c.SetReadDeadline(time.Now())
	}
	return err
}

// Shutdown stops accepting connections, lets in-flight requests complete and
// waits for all connection goroutines to exit. If ctx expires first, the
// remaining connections are closed forcibly and ctx.Err() is returned.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.beginClose()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		s.closeConns()
		<-done
		return ctx.Err()
	}
}

// Close stops the server immediately, closing all connections, and waits for
// connection goroutines to exit.
func (s *Server) Close() error {
	err := s.beginClose()
	s.closeConns()
	s.wg.Wait()
	return err
}

func (s *Server) closeConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.Close()
	}
}

func (s *Server) serveConn(c net.Conn) {
	defer s.untrack(c)
	log := s.log.With("remote", c.RemoteAddr().String())
	log.Info("client connected")

	buf := make([]byte, maxADULen)
	out := make([]byte, 0, maxADULen)
	for {
		// Set the deadline first, then check closing: beginClose sets closing
		// before forcing deadlines, so one of the two always stops us.
		var deadline time.Time
		if s.cfg.IdleTimeout > 0 {
			deadline = time.Now().Add(s.cfg.IdleTimeout)
		}
		_ = c.SetReadDeadline(deadline)
		if s.closing.Load() {
			log.Info("client disconnected", "reason", "server shutdown")
			return
		}

		if _, err := io.ReadFull(c, buf[:mbapHeaderLen]); err != nil {
			s.logReadEnd(log, err)
			return
		}
		h, err := decodeHeader(buf)
		if err != nil {
			log.Warn("invalid MBAP header, closing connection", "err", err)
			return
		}
		pdu := buf[mbapHeaderLen : mbapHeaderLen+h.pduLen()]
		if _, err := io.ReadFull(c, pdu); err != nil {
			s.logReadEnd(log, err)
			return
		}
		if h.ProtocolID != 0 {
			// Not Modbus; the frame was fully consumed, so stay in sync and skip it.
			log.Debug("ignoring frame with non-zero protocol id", "protocol_id", h.ProtocolID)
			continue
		}

		resp, perr := processPDU(s.handler, h.UnitID, pdu)
		if perr != nil {
			lvl := slog.LevelDebug
			if errors.Is(perr, ServerDeviceFailure) || !isException(perr) {
				lvl = slog.LevelError
			}
			log.Log(context.Background(), lvl, "request failed",
				"unit", h.UnitID, "fc", pdu[0], "err", perr)
		}

		out = appendADU(out[:0], h, resp)
		_ = c.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
		if _, err := c.Write(out); err != nil {
			log.Warn("write failed, closing connection", "err", err)
			return
		}
	}
}

func isException(err error) bool {
	var ec ExceptionCode
	return errors.As(err, &ec)
}

func (s *Server) logReadEnd(log *slog.Logger, err error) {
	switch {
	case s.closing.Load():
		log.Info("client disconnected", "reason", "server shutdown")
	case errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed):
		log.Info("client disconnected")
	case errors.Is(err, io.ErrUnexpectedEOF):
		log.Warn("client disconnected mid-frame")
	case errors.Is(err, os.ErrDeadlineExceeded):
		log.Info("client disconnected", "reason", "idle timeout")
	default:
		log.Warn("read failed, closing connection", "err", err)
	}
}
