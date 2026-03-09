package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

const clientBufSize = 256

type client struct {
	conn net.Conn
	ch   chan []byte
}

// Server is a TCP Beast output server that fans out frames
// to all connected clients.
type Server struct {
	port     int
	listener net.Listener
	mu       sync.Mutex
	clients  map[*client]struct{}
	done     chan struct{}
	stopOnce sync.Once

	// welcomeFrames holds opaque byte slices that are pushed to each
	// newly-connected client before it enters the broadcast loop.
	// This allows late-joining clients to receive current status/position
	// frames immediately on connect. Updated atomically so broadcasts
	// are never blocked by a welcome-frame update.
	welcomeFrames atomic.Pointer[[][]byte]
}

func New(port int) *Server {
	return &Server{
		port:    port,
		clients: make(map[*client]struct{}),
		done:    make(chan struct{}),
	}
}

// SetWelcomeFrames stores the frames that will be sent to each new client
// immediately upon connection. The slices are stored as-is; the caller
// must not mutate them after calling this method.
func (s *Server) SetWelcomeFrames(frames [][]byte) {
	s.welcomeFrames.Store(&frames)
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return err
	}
	s.listener = ln
	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.listener.Close()
		s.mu.Lock()
		for c := range s.clients {
			close(c.ch)
		}
		s.clients = make(map[*client]struct{})
		s.mu.Unlock()
	})
}

// AddConn registers an externally-created connection as a client.
// Any configured welcome frames are pushed before the client enters the
// broadcast set, guaranteeing they are a strict prefix of the stream.
func (s *Server) AddConn(conn net.Conn) {
	c := &client{
		conn: conn,
		ch:   make(chan []byte, clientBufSize),
	}

	// Push welcome frames and add to the broadcast set atomically.
	// Welcome frames go into the channel before s.clients insertion,
	// so no concurrent Broadcast can slip a data frame ahead of them.
	s.mu.Lock()
	if wf := s.welcomeFrames.Load(); wf != nil {
		for _, frame := range *wf {
			c.ch <- frame
		}
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	go s.writeLoop(c)
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Broadcast sends a frame to all connected clients.
// Slow clients are skipped (frame dropped) rather than blocking.
func (s *Server) Broadcast(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		select {
		case c.ch <- frame:
		default:
		}
	}
}

func (s *Server) removeClient(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		c.conn.Close()
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		c := &client{
			conn: conn,
			ch:   make(chan []byte, clientBufSize),
		}

		// Push welcome frames and add to the broadcast set atomically.
		// Welcome frames go into the channel before s.clients insertion,
		// so no concurrent Broadcast can slip a data frame ahead of them.
		s.mu.Lock()
		if wf := s.welcomeFrames.Load(); wf != nil {
			for _, frame := range *wf {
				c.ch <- frame
			}
		}
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		log.Printf("client connected: %s (%d total)", conn.RemoteAddr(), s.ClientCount())
		go s.writeLoop(c)
	}
}

func (s *Server) writeLoop(c *client) {
	for frame := range c.ch {
		if _, err := c.conn.Write(frame); err != nil {
			s.removeClient(c)
			return
		}
	}
	c.conn.Close()
}
