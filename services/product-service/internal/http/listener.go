package producthttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	maxRequestLineBytes = 8192
	malformedWriteLimit = 5 * time.Second
)

// Serve keeps malformed percent escapes in request targets from being rejected by net/http's
// built-in parser with a plain-text error. Each connection serves one request so the bounded
// request-line preflight applies to every direct HTTP request.
func Serve(server *http.Server, listener net.Listener) error {
	if server.ReadHeaderTimeout <= 0 {
		return errors.New("product HTTP requires a positive read-header timeout")
	}
	server.SetKeepAlivesEnabled(false)
	return server.Serve(&uriListener{Listener: listener})
}

type uriListener struct{ net.Listener }

func (listener *uriListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &uriConn{Conn: connection, reader: bufio.NewReader(connection)}, nil
}

type uriConn struct {
	net.Conn
	reader  *bufio.Reader
	checked bool
	pending []byte
}

func (connection *uriConn) Read(buffer []byte) (int, error) {
	if !connection.checked {
		connection.checked = true
		if err := connection.checkRequestLine(); err != nil {
			return 0, err
		}
	}
	if len(connection.pending) != 0 {
		count := copy(buffer, connection.pending)
		connection.pending = connection.pending[count:]
		return count, nil
	}
	return connection.reader.Read(buffer)
}

func (connection *uriConn) checkRequestLine() error {
	line := make([]byte, 0, 128)
	for len(line) < maxRequestLineBytes {
		character, err := connection.reader.ReadByte()
		if err != nil {
			connection.pending = line
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
