package easytcp

import (
	"fmt"
	"github.com/DarthPestilane/easytcp/message"
	"github.com/google/uuid"
	"net"
	"sync"
	"time"
)

// Session represents a TCP session.
type Session struct {
	id        string              // session's ID. it's a uuid
	conn      net.Conn            // tcp connection
	closeOnce sync.Once           // to make sure we can only close each session one time
	closed    chan struct{}       // to close()
	reqQueue  chan *message.Entry // request queue channel, pushed in ReadLoop() and popped in router.Router
	respQueue chan *message.Entry // response queue channel, pushed in SendResp() and popped in WriteLoop()
	packer    Packer              // to pack and unpack message
	codec     Codec               // encode/decode message data
}

// SessionOption is the extra options for Session.
type SessionOption struct {
	Packer          Packer
	Codec           Codec
	ReadBufferSize  int
	WriteBufferSize int
}

// NewSession creates a new Session.
// Parameter conn is the TCP connection,
// opt includes packer, codec, and channel size.
// Returns a Session pointer.
func NewSession(conn net.Conn, opt *SessionOption) *Session {
	id := uuid.NewString()
	return &Session{
		id:        id,
		conn:      conn,
		closed:    make(chan struct{}),
		reqQueue:  make(chan *message.Entry, opt.ReadBufferSize),
		respQueue: make(chan *message.Entry, opt.WriteBufferSize),
		packer:    opt.Packer,
		codec:     opt.Codec,
	}
}

// ID implements the Session ID method.
// Returns session's ID.
func (s *Session) ID() string {
	return s.id
}

// Codec implements the Session Codec method.
// Returns the message codec bound to session.
func (s *Session) Codec() Codec {
	return s.codec
}

// RecvReq implements the Session RecvReq method.
// Returns reqQueue channel which contains MessageEntry.
func (s *Session) RecvReq() <-chan *message.Entry {
	return s.reqQueue
}

// SendResp implements the Session SendResp method.
// If respQueue is closed, returns false.
func (s *Session) SendResp(respMsg *message.Entry) error {
	if !s.safelyPushRespQueue(respMsg) {
		return fmt.Errorf("session's closed")
	}
	return nil
}

// Close closes the session by closing all the channels.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		close(s.reqQueue)
		close(s.respQueue)
	})
}

// ReadLoop reads TCP connection, unpacks packet payload
// to a MessageEntry, and push to reqQueue channel.
// The above operations are in a loop.
// Parameter readTimeout specified the connection reading timeout.
// The loop will break if any error occurred, or the session is closed.
// After loop ended, this session will be closed.
func (s *Session) ReadLoop(readTimeout time.Duration) {
	for {
		if readTimeout > 0 {
			if err := s.conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				Log.Tracef("set read deadline err: %s", err)
				break
			}
		}
		entry, err := s.packer.Unpack(s.conn)
		if err != nil {
			Log.Tracef("unpack incoming message err: %s", err)
			break
		}
		if !s.safelyPushReqQueue(entry) {
			break
		}
	}
	Log.Tracef("read loop exit")
	s.Close()
}

// WriteLoop fetches message from respQueue channel and writes to TCP connection.
// The above operations are in a loop.
// Parameter writeTimeout specified the connection writing timeout.
// The loop will break if any error occurred, or the session is closed.
// After loop ended, this session will be closed.
func (s *Session) WriteLoop(writeTimeout time.Duration) {
	for {
		respMsg, ok := <-s.respQueue
		if !ok {
			break
		}
		// pack message
		ackMsg, err := s.packer.Pack(respMsg)
		if err != nil {
			Log.Tracef("pack response message err: %s", err)
			continue
		}
		if writeTimeout > 0 {
			if err := s.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				Log.Tracef("set write deadline err: %s", err)
				break
			}
		}
		if _, err := s.conn.Write(ackMsg); err != nil {
			Log.Tracef("conn write err: %s", err)
			break
		}
	}
	Log.Tracef("write loop exit")
	s.Close()
}

// WaitUntilClosed waits until the session is closed.
func (s *Session) WaitUntilClosed() {
	<-s.closed
}

func (s *Session) safelyPushReqQueue(reqMsg *message.Entry) (ok bool) {
	ok = true
	defer func() {
		if r := recover(); r != nil {
			ok = false
			Log.Tracef("push reqQueue panics: %+v", r)
		}
	}()
	s.reqQueue <- reqMsg
	return ok
}

func (s *Session) safelyPushRespQueue(respMsg *message.Entry) (ok bool) {
	ok = true
	defer func() {
		if r := recover(); r != nil {
			ok = false
			Log.Tracef("push respQueue panics: %+v", r)
		}
	}()
	s.respQueue <- respMsg
	return ok
}
