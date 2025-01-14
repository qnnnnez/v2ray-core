package mux

import (
	"sync"

	"v2ray.com/core/common"
	"v2ray.com/core/common/buf"
	"v2ray.com/core/common/protocol"
)

type SessionManager struct {
	sync.RWMutex
	sessions    map[uint16]*Session
	count       uint16
	closed      bool
	requestChan chan uint16
}

func NewSessionManager() *SessionManager {
	result := SessionManager{
		count:       0,
		sessions:    make(map[uint16]*Session, 16),
		requestChan: make(chan uint16),
	}

	go result.sendToken()

	return &result
}

func (m *SessionManager) sendToken() {
	for {
		m.RLock()
		var hit = false
		for _, session := range m.sessions {
			select {
			case session.tokenChan <- 233:
				hit = true
			default:
			}
		}
		m.RUnlock()
		if !hit {
			_, more := <-m.requestChan
			if !more {
				break
			}
		}
	}
}

func (m *SessionManager) Closed() bool {
	m.RLock()
	defer m.RUnlock()

	return m.closed
}

func (m *SessionManager) Size() int {
	m.RLock()
	defer m.RUnlock()

	return len(m.sessions)
}

func (m *SessionManager) Count() int {
	m.RLock()
	defer m.RUnlock()

	return int(m.count)
}

func (m *SessionManager) Allocate() *Session {
	m.Lock()
	defer m.Unlock()

	if m.closed {
		return nil
	}

	m.count++
	s := &Session{
		ID:        m.count,
		parent:    m,
		tokenChan: make(chan uint16),
	}
	m.sessions[s.ID] = s
	return s
}

func (m *SessionManager) Add(s *Session) {
	m.Lock()
	defer m.Unlock()

	if m.closed {
		return
	}

	s.tokenChan = make(chan uint16)

	m.count++
	m.sessions[s.ID] = s
}

func (m *SessionManager) Remove(id uint16) {
	m.Lock()
	defer m.Unlock()

	if m.closed {
		return
	}

	delete(m.sessions, id)

	if len(m.sessions) == 0 {
		m.sessions = make(map[uint16]*Session, 16)
	}
}

func (m *SessionManager) Get(id uint16) (*Session, bool) {
	m.RLock()
	defer m.RUnlock()

	if m.closed {
		return nil, false
	}

	s, found := m.sessions[id]
	return s, found
}

func (m *SessionManager) CloseIfNoSession() bool {
	m.Lock()
	defer m.Unlock()

	if m.closed {
		return true
	}

	if len(m.sessions) != 0 {
		return false
	}

	m.closed = true
	return true
}

func (m *SessionManager) Close() error {
	m.Lock()
	defer m.Unlock()

	if m.closed {
		return nil
	}

	m.closed = true
	close(m.requestChan)

	for _, s := range m.sessions {
		common.Close(s.input)  // nolint: errcheck
		common.Close(s.output) // nolint: errcheck
	}

	m.sessions = nil
	return nil
}

// Session represents a client connection in a Mux connection.
type Session struct {
	input        buf.Reader
	output       buf.Writer
	parent       *SessionManager
	ID           uint16
	transferType protocol.TransferType
	tokenChan    chan uint16
}

// Close closes all resources associated with this session.
func (s *Session) Close() error {
	common.Close(s.output) // nolint: errcheck
	common.Close(s.input)  // nolint: errcheck
	s.parent.Remove(s.ID)
	return nil
}

// NewReader creates a buf.Reader based on the transfer type of this Session.
func (s *Session) NewReader(reader *buf.BufferedReader) buf.Reader {
	if s.transferType == protocol.TransferTypeStream {
		return NewStreamReader(reader)
	}
	return NewPacketReader(reader)
}

func (s *Session) ReadMultiBuffer() (buf.MultiBuffer, error) {
	select {
	case s.parent.requestChan <- s.ID:
		break
	default:
		<-s.tokenChan
	}
	buf, err := s.input.ReadMultiBuffer()
	return buf, err
}
