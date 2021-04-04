package redcon

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"

	quic "github.com/lucas-clemente/quic-go"
)

type streamInfo struct {
	id     quic.StreamID
	stream quic.Stream
}

// conn represents a client connection
type quicConn struct {
	session    quic.Session
	streamPool map[quic.StreamID]streamInfo
	wr         *Writer
	rd         *Reader
	addr       string
	ctx        interface{}
	detached   bool
	closed     bool
	cmds       []Command
	idleClose  time.Duration
}

func (c *quicConn) Detach() DetachedConn {
	c.detached = true
	return &quicDetachedConn{quicConn: c}
}

func (c *quicConn) Start() error {
	for {
		stream, err := c.session.AcceptStream(context.Background())
		if err != nil {
			fmt.Println(err)
			return err
		}
		//c.streamPool[stream.StreamID()] = streamInfo{id: stream.StreamID(), stream: stream}
		c.rd = NewReader(stream)
		c.wr = NewWriter(stream)
		break
	}
	return nil
}

func (c *quicConn) Close() error {
	c.wr.Flush()
	c.closed = true
	return c.session.CloseWithError(0, "close")
}
func (c *quicConn) Context() interface{}        { return c.ctx }
func (c *quicConn) SetContext(v interface{})    { c.ctx = v }
func (c *quicConn) SetReadBuffer(n int)         {}
func (c *quicConn) WriteString(str string)      { c.wr.WriteString(str) }
func (c *quicConn) WriteBulk(bulk []byte)       { c.wr.WriteBulk(bulk) }
func (c *quicConn) WriteBulkString(bulk string) { c.wr.WriteBulkString(bulk) }
func (c *quicConn) WriteInt(num int)            { c.wr.WriteInt(num) }
func (c *quicConn) WriteInt64(num int64)        { c.wr.WriteInt64(num) }
func (c *quicConn) WriteUint64(num uint64)      { c.wr.WriteUint64(num) }
func (c *quicConn) WriteError(msg string)       { c.wr.WriteError(msg) }
func (c *quicConn) WriteArray(count int)        { c.wr.WriteArray(count) }
func (c *quicConn) WriteNull()                  { c.wr.WriteNull() }
func (c *quicConn) WriteRaw(data []byte)        { c.wr.WriteRaw(data) }
func (c *quicConn) WriteAny(v interface{})      { c.wr.WriteAny(v) }
func (c *quicConn) RemoteAddr() string          { return c.addr }
func (c *quicConn) ReadPipeline() []Command {
	return nil
}
func (c *quicConn) PeekPipeline() []Command {
	return nil
}
func (c *quicConn) NetConn() net.Conn {
	return nil
}

// QUICServer defines a quic server for clients for managing client connections.
// only support DetachedConn, and it means that handler cb not used
// how use, please refer to example/clone.go 'quic'
type QUICServer struct {
	mu        sync.Mutex
	net       string
	laddr     string
	handler   func(conn Conn, cmd Command)
	accept    func(conn Conn) bool
	closed    func(conn Conn, err error)
	conns     map[*quicConn]bool
	ln        quic.Listener
	done      bool
	idleClose time.Duration

	// AcceptError is an optional function used to handle Accept errors.
	AcceptError func(err error)
}

// Addr returns server's listen address
func (s *QUICServer) Addr() net.Addr {
	return s.ln.Addr()
}

// ListenAndServe serves incoming connections.
func (s *QUICServer) ListenAndServe() error {
	return s.ListenServeAndSignal(nil)
}

// ListenServeAndSignal serves incoming connections and passes nil or error
// when listening. signal can be nil.
func (s *QUICServer) ListenServeAndSignal(signal chan error) error {
	cfg := quic.Config{}
	cfg.MaxIncomingStreams = 1
	cfg.MaxIncomingUniStreams = 1
	ln, err := quic.ListenAddr(s.laddr, generateTLSConfig(), &cfg)
	if err != nil {
		if signal != nil {
			signal <- err
		}
		return err
	}
	s.ln = ln
	if signal != nil {
		signal <- nil
	}
	return quicServe(s)
}

func (s *QUICServer) SetIdleClose(dur time.Duration) {
	s.mu.Lock()
	s.idleClose = dur
	s.mu.Unlock()
}

func quicServe(s *QUICServer) error {
	defer func() {
		s.ln.Close()
		func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for c := range s.conns {
				c.Close()
			}
			s.conns = nil
		}()
	}()
	for {
		sess, err := s.ln.Accept(context.Background())
		if err != nil {
			return err
		}
		if err != nil {
			s.mu.Lock()
			done := s.done
			s.mu.Unlock()
			if done {
				return nil
			}
			if s.AcceptError != nil {
				s.AcceptError(err)
			}
			continue
		}
		c := &quicConn{
			session: sess,
			addr:    sess.RemoteAddr().String(),
		}
		s.mu.Lock()
		c.idleClose = s.idleClose
		s.conns[c] = true
		s.mu.Unlock()
		if s.accept != nil && !s.accept(c) {
			s.mu.Lock()
			delete(s.conns, c)
			s.mu.Unlock()
			c.Close()
			continue
		}
		go quicHandle(s, c)
	}
}
func quicHandle(s *QUICServer, c *quicConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

func (s *QUICServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return errors.New("not serving")
	}
	s.done = true
	return s.ln.Close()
}

// Setup a bare-bones TLS config for the server
func generateTLSConfig() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"quic-echo-example"},
	}
}

type quicDetachedConn struct {
	*quicConn
}

// Flush writes and Write* calls to the client.
func (dc *quicDetachedConn) Flush() error {
	return dc.wr.Flush()
}

func (dc *quicDetachedConn) ReadMyCommand() (Command, error) {
	args, err := readMyCommand(dc.rd.rd)
	if err != nil {
		return Command{}, err
	}
	return Command{Args: args}, nil
}

func (dc *quicDetachedConn) Reclaim(cmd *Command) {
	if cmd == nil {
		return
	}
	for i := range cmd.Args {
		putBuf(cmd.Args[i][:0])
		cmd.Args[i] = nil
	}
	cmd.Args = cmd.Args[:0]
	putBufs(cmd.Args)
}

// ReadCommand read the next command from the client.
func (dc *quicDetachedConn) ReadCommand() (Command, error) {
	return Command{}, fmt.Errorf("not supported")
}
