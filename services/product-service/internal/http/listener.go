package producthttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	maxRequestLineBytes   = 8192
	malformedWriteLimit   = 5 * time.Second
	defaultMaxHeaderBytes = 1 << 20
	headerReadSlop        = 4096
)

// Serve keeps malformed percent escapes in request targets from being rejected by net/http's
// built-in parser with a plain-text error while preserving bounded persistent connections.
func Serve(server *http.Server, listener net.Listener) error {
	if server.ReadHeaderTimeout <= 0 {
		return errors.New("product HTTP requires a positive read-header timeout")
	}
	if server.IdleTimeout <= 0 {
		return errors.New("product HTTP requires a positive idle timeout")
	}
	wrapServerHandler(server)
	maxHeaderBytes := server.MaxHeaderBytes
	if maxHeaderBytes <= 0 {
		maxHeaderBytes = defaultMaxHeaderBytes
	}
	return server.Serve(&uriListener{
		Listener:       listener,
		maxHeaderBytes: maxHeaderBytes + headerReadSlop,
	})
}

type uriConnContextKey struct{}

func wrapServerHandler(server *http.Server) {
	handler := server.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	connContext := server.ConnContext
	server.ConnContext = func(ctx context.Context, connection net.Conn) context.Context {
		if connContext != nil {
			ctx = connContext(ctx, connection)
		}
		if uriConnection, ok := connection.(*uriConn); ok {
			ctx = context.WithValue(ctx, uriConnContextKey{}, uriConnection)
		}
		return ctx
	}
	server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, _ := request.Context().Value(uriConnContextKey{}).(*uriConn)
		reusable := request.ContentLength <= 0 && len(request.TransferEncoding) == 0 &&
			connection != nil
		if !reusable {
			request.Close = true
			if connection != nil {
				connection.disablePreflight()
			}
		}
		handler.ServeHTTP(writer, request)
	})
}

type uriListener struct {
	net.Listener
	maxHeaderBytes int
}

func (listener *uriListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &uriConn{
		Conn:           connection,
		reader:         bufio.NewReader(connection),
		maxHeaderBytes: listener.maxHeaderBytes,
	}, nil
}

type uriConn struct {
	net.Conn
	reader         *bufio.Reader
	checked        bool
	pending        []byte
	pendingErr     error
	maxHeaderBytes int
	passThrough    bool
}

func (connection *uriConn) disablePreflight() {
	connection.passThrough = true
}

func (connection *uriConn) Read(buffer []byte) (int, error) {
	if len(connection.pending) != 0 {
		count := copy(buffer, connection.pending)
		connection.pending = connection.pending[count:]
		return count, nil
	}
	if connection.pendingErr != nil {
		err := connection.pendingErr
		connection.pendingErr = nil
		connection.checked = false
		return 0, err
	}
	if connection.passThrough {
		return connection.reader.Read(buffer)
	}
	if !connection.checked {
		connection.checked = true
		if err := connection.checkRequestLine(); err != nil {
			connection.checked = false
			return 0, err
		}
	}
	if len(connection.pending) != 0 {
		count := copy(buffer, connection.pending)
		connection.pending = connection.pending[count:]
		return count, nil
	}
	return connection.readHeaders(buffer)
}

func (connection *uriConn) readHeaders(buffer []byte) (int, error) {
	headers := make([]byte, 0, 256)
	for len(headers) <= connection.maxHeaderBytes {
		line, err := connection.reader.ReadSlice('\n')
		headers = append(headers, line...)
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err != nil:
			connection.pendingErr = err
		case bytes.Equal(line, []byte("\r\n")) || bytes.Equal(line, []byte("\n")):
			connection.checked = false
		default:
			continue
		}
		break
	}
	if len(headers) > connection.maxHeaderBytes {
		connection.passThrough = true
	}
	connection.pending = headers
	if len(connection.pending) == 0 {
		if connection.pendingErr != nil {
			err := connection.pendingErr
			connection.pendingErr = nil
			connection.checked = false
			return 0, err
		}
		return connection.reader.Read(buffer)
	}
	count := copy(buffer, connection.pending)
	connection.pending = connection.pending[count:]
	return count, nil
}

func (connection *uriConn) checkRequestLine() error {
	line := make([]byte, 0, 128)
	for len(line) < maxRequestLineBytes {
		character, err := connection.reader.ReadByte()
		if err != nil {
			if len(line) == 0 {
				return err
			}
			connection.pending = line
			connection.pendingErr = err
			return nil // Let net/http handle incomplete or otherwise invalid requests.
		}
		line = append(line, character)
		if character == '\n' {
			break
		}
	}
	if len(line) == maxRequestLineBytes && line[len(line)-1] != '\n' {
		_ = connection.SetWriteDeadline(time.Now().Add(malformedWriteLimit))
		_, _ = io.WriteString(
			connection,
			"HTTP/1.1 414 URI Too Long\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		)
		_ = connection.Close()
		return io.EOF
	}
	connection.pending = line
	path, ok := requestPath(line)
	if !ok {
		return nil
	}
	index := malformedEscapeIndex(path)
	if index < 0 {
		return nil
	}

	message := fmt.Sprintf("Malformed URI: Malformed escape pair at index %d: %s", index, path)
	body := jsonErrorBody(message, "/")
	response := fmt.Sprintf(
		"HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		len(body),
	)
	_ = connection.SetWriteDeadline(time.Now().Add(malformedWriteLimit))
	writer := bufio.NewWriter(connection)
	_, writeErr := writer.WriteString(response)
	if writeErr == nil {
		_, writeErr = writer.Write(body)
	}
	if writeErr == nil {
		writeErr = writer.Flush()
	}
	_ = connection.Close()
	if writeErr != nil {
		return writeErr
	}
	return io.EOF
}

func requestPath(line []byte) (string, bool) {
	firstSpace := bytes.IndexByte(line, ' ')
	if firstSpace < 0 {
		return "", false
	}
	rest := line[firstSpace+1:]
	secondSpace := bytes.IndexByte(rest, ' ')
	if secondSpace < 0 {
		return "", false
	}
	target := string(rest[:secondSpace])
	if !strings.HasPrefix(target, "/") {
		return "", false
	}
	path, _, _ := strings.Cut(target, "?")
	return path, true
}

func malformedEscapeIndex(path string) int {
	for index := 0; index < len(path); index++ {
		if path[index] != '%' {
			continue
		}
		if index+2 >= len(path) || !isHex(path[index+1]) || !isHex(path[index+2]) {
			return index
		}
		index += 2
	}
	return -1
}

func isHex(character byte) bool {
	return character >= '0' && character <= '9' ||
		character >= 'a' && character <= 'f' ||
		character >= 'A' && character <= 'F'
}
