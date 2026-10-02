package remotedialer

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	errFailedAuth       = errors.New("failed authentication")
	errWrongMessageType = errors.New("wrong websocket message type")
)

type Authorizer func(req *http.Request) (clientKey string, authed bool, err error)
type ErrorWriter func(rw http.ResponseWriter, req *http.Request, code int, err error)

func DefaultErrorWriter(rw http.ResponseWriter, req *http.Request, code int, err error) {
	rw.WriteHeader(code)
	rw.Write([]byte(err.Error()))
}

type Server struct {
	// OnSessionChange (sextant patch) is called after a client's session is
	// added or removed, with whether the client still has any live session.
	// It runs on the connection's goroutine and must not block.
	OnSessionChange func(clientKey string, connected bool)
	notifyMu        sync.Mutex // orders OnSessionChange calls with the state they report

	PeerID                  string
	PeerToken               string
	ClientConnectAuthorizer ConnectAuthorizer
	authorizer              Authorizer
	errorWriter             ErrorWriter
	sessions                *sessionManager
	peers                   map[string]peer
	peerLock                sync.Mutex
}

func New(auth Authorizer, errorWriter ErrorWriter) *Server {
	return &Server{
		peers:       map[string]peer{},
		authorizer:  auth,
		errorWriter: errorWriter,
		sessions:    newSessionManager(),
	}
}

func (s *Server) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	clientKey, authed, peer, err := s.auth(req)
	if err != nil {
		s.errorWriter(rw, req, 400, err)
		return
	}
	if !authed {
		s.errorWriter(rw, req, 401, errFailedAuth)
		return
	}

	logrus.Infof("Handling backend connection request [%s]", clientKey)

	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin:      func(r *http.Request) bool { return true },
		Error:            s.errorWriter,
	}

	wsConn, err := upgrader.Upgrade(rw, req, nil)
	if err != nil {
		s.errorWriter(rw, req, 400, errors.Wrapf(err, "Error during upgrade for host [%v]", clientKey))
		return
	}

	session := s.sessions.add(clientKey, wsConn, peer)
	session.auth = s.ClientConnectAuthorizer
	if !peer {
		s.notifySessionChange(clientKey)
	}
	defer func() {
		s.sessions.remove(session)
		if !peer {
			s.notifySessionChange(clientKey)
		}
	}()

	code, err := session.Serve(req.Context())
	if err != nil {
		// Hijacked so we can't write to the client
		logrus.Infof("error in remotedialer server [%d]: %v", code, err)
	}
}

// Disconnect drops every live session of clientKey, e.g. after its credential
// is revoked. It returns the number of sessions closed (sextant patch).
func (s *Server) Disconnect(clientKey string) int { return s.sessions.disconnect(clientKey) }

func (s *Server) ListClients() []string {
	return s.sessions.listClients()
}

func (s *Server) auth(req *http.Request) (clientKey string, authed, peer bool, err error) {
	id := req.Header.Get(ID)
	token := req.Header.Get(Token)
	if id != "" && token != "" {
		// peer authentication
		s.peerLock.Lock()
		p, ok := s.peers[id]
		s.peerLock.Unlock()

		if ok && p.token == token {
			return id, true, true, nil
		}
	}

	id, authed, err = s.authorizer(req)
	return id, authed, false, err
}

// notifySessionChange evaluates the state and delivers it under one lock, so
// two connections of the same client cannot deliver their states out of order
// (a stale "disconnected" landing after a newer "connected").
func (s *Server) notifySessionChange(clientKey string) {
	if s.OnSessionChange == nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	s.OnSessionChange(clientKey, s.HasSession(clientKey))
}
