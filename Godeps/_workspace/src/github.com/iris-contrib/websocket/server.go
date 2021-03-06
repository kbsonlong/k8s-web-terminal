package websocket

import (
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/beyondblog/k8s-web-terminal/Godeps/_workspace/src/github.com/kataras/iris/context"
	"github.com/beyondblog/k8s-web-terminal/Godeps/_workspace/src/github.com/valyala/fasthttp"
)

//taken from net/http
const (
	StatusContinue           = 100
	StatusSwitchingProtocols = 101

	StatusOK                   = 200
	StatusCreated              = 201
	StatusAccepted             = 202
	StatusNonAuthoritativeInfo = 203
	StatusNoContent            = 204
	StatusResetContent         = 205
	StatusPartialContent       = 206

	StatusMultipleChoices   = 300
	StatusMovedPermanently  = 301
	StatusFound             = 302
	StatusSeeOther          = 303
	StatusNotModified       = 304
	StatusUseProxy          = 305
	StatusTemporaryRedirect = 307

	StatusBadRequest                   = 400
	StatusUnauthorized                 = 401
	StatusPaymentRequired              = 402
	StatusForbidden                    = 403
	StatusNotFound                     = 404
	StatusMethodNotAllowed             = 405
	StatusNotAcceptable                = 406
	StatusProxyAuthRequired            = 407
	StatusRequestTimeout               = 408
	StatusConflict                     = 409
	StatusGone                         = 410
	StatusLengthRequired               = 411
	StatusPreconditionFailed           = 412
	StatusRequestEntityTooLarge        = 413
	StatusRequestURITooLong            = 414
	StatusUnsupportedMediaType         = 415
	StatusRequestedRangeNotSatisfiable = 416
	StatusExpectationFailed            = 417
	StatusTeapot                       = 418
	StatusPreconditionRequired         = 428
	StatusTooManyRequests              = 429
	StatusRequestHeaderFieldsTooLarge  = 431
	StatusUnavailableForLegalReasons   = 451

	StatusInternalServerError           = 500
	StatusNotImplemented                = 501
	StatusBadGateway                    = 502
	StatusServiceUnavailable            = 503
	StatusGatewayTimeout                = 504
	StatusHTTPVersionNotSupported       = 505
	StatusNetworkAuthenticationRequired = 511
)

// HandshakeError describes an error with the handshake from the peer.
type HandshakeError struct {
	message string
}

func (e HandshakeError) Error() string { return e.message }

// Upgrader specifies parameters for upgrading an HTTP connection to a
// WebSocket connection.
type Upgrader struct {
	// HandshakeTimeout specifies the duration for the handshake to complete.
	HandshakeTimeout time.Duration

	// ReadBufferSize and WriteBufferSize specify I/O buffer sizes. If a buffer
	// size is zero, then a default value of 4096 is used. The I/O buffer sizes
	// do not limit the size of the messages that can be sent or received.
	ReadBufferSize, WriteBufferSize int

	// Subprotocols specifies the server's supported protocols in order of
	// preference. If this field is set, then the Upgrade method negotiates a
	// subprotocol by selecting the first match in this list with a protocol
	// requested by the client.
	Subprotocols []string

	// Error specifies the function for generating HTTP error responses.
	Error func(ctx context.IContext, status int, reason error)

	// CheckOrigin returns true if the request Origin header is acceptable. If
	// CheckOrigin is nil, the host in the Origin header must not be set or
	// must match the host of the request.
	CheckOrigin func(ctx context.IContext) bool

	//Receiver it's the receiver handler, acceps a *websocket.Conn
	Receiver func(*Conn)

	// Headers  if true then the client's headers are copy to the websocket connection
	Headers bool
}

// DontCheckOrigin set Upgrader.CheckOrigin to a function which always returns true
// returns itself
func (u *Upgrader) DontCheckOrigin() *Upgrader {
	u.CheckOrigin = func(ctx context.IContext) bool {
		return true
	}
	return u
}

func (u *Upgrader) returnError(ctx context.IContext, status int, reason string) error {
	err := HandshakeError{reason}
	if u.Error != nil {
		u.Error(ctx, status, err)
	} else {
		ctx.EmitError(status)
	}
	return err
}

// checkSameOrigin returns true if the origin is not set or is equal to the request host.
func checkSameOrigin(ctx context.IContext) bool {
	origin := ctx.RequestHeader("Origin")
	if len(origin) == 0 {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == string(ctx.HostString())
}

func (u *Upgrader) selectSubprotocol(ctx context.IContext) string {
	responseHeader := ctx.GetRequestCtx().Response.Header
	if u.Subprotocols != nil {
		clientProtocols := Subprotocols(ctx)
		for _, serverProtocol := range u.Subprotocols {
			for _, clientProtocol := range clientProtocols {
				if clientProtocol == serverProtocol {
					return clientProtocol
				}
			}
		}
	} else if responseHeader.Len() > 0 {
		return string(responseHeader.Peek("Sec-Websocket-Protocol"))
	}
	return ""
}

func (u *Upgrader) getSubprotocol(ctx context.IContext) (subprotocol string) {
	//first of all check if we have already that setted
	if h := string(ctx.GetRequestCtx().Response.Header.Peek("Sec-Websocket-Protocol")); h != "" {
		subprotocol = h
		return
	}

	header := string(ctx.GetRequestCtx().Request.Header.Peek("Sec-Websocket-Protocol"))
	if len(header) > 0 {
		protocols := strings.Split(header, ",")
		for i := range protocols {
			protocols[i] = strings.TrimSpace(protocols[i])
		}

		if len(protocols) > 0 {
			subprotocol = checkSubprotocols(protocols, u.Subprotocols)
			if subprotocol != "" {
				ctx.GetRequestCtx().Response.Header.Set("Sec-Websocket-Protocol", subprotocol)
			}
		}
	}

	return
}

func checkSubprotocols(reqProtocols []string, resProtocols []string) string {
	for _, resProtocol := range resProtocols {
		for _, reqProtocol := range reqProtocols {
			if reqProtocol == resProtocol {
				return reqProtocol
			}
		}
	}

	return ""
}

// Upgrade upgrades the HTTP server connection to the WebSocket protocol.
//
// The responseHeader is included in the response to the client's upgrade
// request. Use the responseHeader to specify cookies (Set-Cookie) and the
// application negotiated subprotocol (Sec-Websocket-Protocol).
//
// If the upgrade fails, then Upgrade replies to the client with an HTTP error
// response.
func (u *Upgrader) Upgrade(ctx context.IContext) error {
	if !ctx.GetRequestCtx().IsGet() {
		return u.returnError(ctx, StatusMethodNotAllowed, "websocket: method not GET")
	}
	if ctx.RequestHeader("Sec-Websocket-Version") != "13" {
		return u.returnError(ctx, StatusBadRequest, "websocket: version != 13")
	}

	if !ctx.GetRequestCtx().Request.Header.ConnectionUpgrade() {
		return u.returnError(ctx, StatusBadRequest, "websocket: could not find connection header with token 'upgrade'")
	}

	if !tokenListContainsValue(ctx.RequestHeader("Upgrade"), "websocket") {
		return u.returnError(ctx, StatusBadRequest, "websocket: could not find upgrade header with token 'websocket'")
	}

	checkOrigin := u.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = checkSameOrigin
	}
	if !checkOrigin(ctx) {
		return u.returnError(ctx, StatusForbidden, "websocket: origin not allowed")
	}

	challengeKey := ctx.RequestHeader("Sec-Websocket-Key")
	if challengeKey == "" {
		return u.returnError(ctx, StatusBadRequest, "websocket: key missing or blank")
	}

	//set the headers
	ctx.SetStatusCode(StatusSwitchingProtocols)
	ctx.GetRequestCtx().Response.Header.Set("Upgrade", "websocket")
	ctx.GetRequestCtx().Response.Header.Set("Connection", "Upgrade")
	ctx.GetRequestCtx().Response.Header.Set("Sec-Websocket-Accept", computeAcceptKey(challengeKey))

	subprotocol := u.selectSubprotocol(ctx)

	/*

		var (
			netConn net.Conn
			br      *bufio.Reader
			err     error
		)

		h, ok := w.(fasthttp.Hijacker)
		if !ok {
			return u.returnError(ctx, http.StatusInternalServerError, "websocket: response does not implement http.Hijacker")
		}
		var rw *bufio.ReadWriter
		netConn, rw, err = h.Hijack()
		if err != nil {
			return u.returnError(ctx, http.StatusInternalServerError, err.Error())
		}
		br = rw.Reader

		if br.Buffered() > 0 {
			netConn.Close()
			return nil, errors.New("websocket: client sent data before handshake is complete")
		}
		   c := newConn(netConn, true, u.ReadBufferSize, u.WriteBufferSize)
		   	c.subprotocol = subprotocol

		   	p := c.writeBuf[:0]
		   	p = append(p, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "...)
		   	p = append(p, computeAcceptKey(challengeKey)...)
		   	p = append(p, "\r\n"...)
		   	if c.subprotocol != "" {
		   		p = append(p, "Sec-Websocket-Protocol: "...)
		   		p = append(p, c.subprotocol...)
		   		p = append(p, "\r\n"...)
		   	}
		   	for k, vs := range responseHeader {
		   		if k == protocolHeader {
		   			continue
		   		}
		   		for _, v := range vs {
		   			p = append(p, k...)
		   			p = append(p, ": "...)
		   			for i := 0; i < len(v); i++ {
		   				b := v[i]
		   				if b <= 31 {
		   					// prevent response splitting.
		   					b = ' '
		   				}
		   				p = append(p, b)
		   			}
		   			p = append(p, "\r\n"...)
		   		}
		   	}
		   	p = append(p, "\r\n"...)

		   	// Clear deadlines set by HTTP server.
		   	netConn.SetDeadline(time.Time{})

		   	if u.HandshakeTimeout > 0 {
		   		netConn.SetWriteDeadline(time.Now().Add(u.HandshakeTimeout))
		   	}
		   	if _, err = netConn.Write(p); err != nil {
		   		netConn.Close()
		   		return nil, err
		   	}
		   	if u.HandshakeTimeout > 0 {
		   		netConn.SetWriteDeadline(time.Time{})
		   	}
	*/
	h := &fasthttp.RequestHeader{}
	if u.Headers {
		//copy request headers in order to have access inside the Conn after
		ctx.GetRequestCtx().Request.Header.CopyTo(h)
	}
	ctx.GetRequestCtx().Hijack(func(conn net.Conn) {
		c := newConn(conn, true, u.ReadBufferSize, u.WriteBufferSize)
		if u.Headers {
			c.SetHeaders(h)
		}
		c.subprotocol = subprotocol
		u.Receiver(c)

	})

	return nil
}

// Upgrade upgrades the HTTP server connection to the WebSocket protocol.
//
// If the endpoint supports subprotocols, then the application is responsible
// for negotiating the protocol used on the connection. Use the Subprotocols()
// function to get the subprotocols requested by the client. Use the
// Sec-Websocket-Protocol response header to specify the subprotocol selected
// by the application.
//
// The responseHeader is included in the response to the client's upgrade
// request. Use the responseHeader to specify cookies (Set-Cookie) and the
// negotiated subprotocol (Sec-Websocket-Protocol).
//
// The connection buffers IO to the underlying network connection. The
// readBufSize and writeBufSize parameters specify the size of the buffers to
// use. Messages can be larger than the buffers.
//
// If the request is not a valid WebSocket handshake, then Upgrade returns an
// error of type HandshakeError. Applications should handle this error by
// replying to the client with an HTTP error response.
func Upgrade(ctx context.IContext, receiverHandler func(*Conn), readBufSize, writeBufSize int) error {
	u := Upgrader{ReadBufferSize: readBufSize, WriteBufferSize: writeBufSize, Receiver: receiverHandler}
	u.Error = func(ctx context.IContext, status int, reason error) {
		// don't return errors to maintain backwards compatibility
	}
	u.CheckOrigin = func(ctx context.IContext) bool {
		// allow all connections by default
		return true
	}
	return u.Upgrade(ctx)
}

// Custom returns an Upgrader with customized options (readBufSize,writeBuf size int)
// accepts 3 parameters
// first parameter is the receiver, think it like a handler which accepts a *websocket.Conn (func *websocket.Conn)
// second parameter is the readBufSize (int)
// third parameter is the writeBufSize (int)
func Custom(receiverHandler func(*Conn), readBufSize, writeBufSize int, copyheaders bool) Upgrader {
	u := Upgrader{ReadBufferSize: readBufSize, WriteBufferSize: writeBufSize, Receiver: receiverHandler, Headers: copyheaders}
	u.Error = func(ctx context.IContext, status int, reason error) {
		// don't return errors to maintain backwards compatibility
	}
	u.CheckOrigin = func(ctx context.IContext) bool {
		// allow all connections by default
		return true
	}
	return u
}

// New returns an Upgrader with the default options
// accepts one parameter
// the receiver, think it like a handler which accepts a *websocket.Conn (func *websocket.Conn)
func New(receiverHandler func(*Conn)) Upgrader {
	return Custom(receiverHandler, 4096, 4096, true)
}

// Subprotocols returns the subprotocols requested by the client in the
// Sec-Websocket-Protocol header.
func Subprotocols(ctx context.IContext) []string {
	h := strings.TrimSpace(ctx.RequestHeader("Sec-Websocket-Protocol"))
	if h == "" {
		return nil
	}
	protocols := strings.Split(h, ",")
	for i := range protocols {
		protocols[i] = strings.TrimSpace(protocols[i])
	}
	return protocols
}

// IsWebSocketUpgrade returns true if the client requested upgrade to the
// WebSocket protocol.
func IsWebSocketUpgrade(ctx context.IContext) bool {
	return tokenListContainsValue(ctx.RequestHeader("Connection"), "upgrade") &&
		tokenListContainsValue(ctx.RequestHeader("Upgrade"), "websocket")
}
