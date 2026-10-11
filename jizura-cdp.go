package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Minimal WebSocket/CDP client. The native CLI has no Node/Python or Go-module
// runtime dependencies. Chromium's CDP endpoint is always loopback and ws://.
type cdpSocket struct {
	conn net.Conn
	rd   *bufio.Reader
	next int
}

func connectCDP(endpoint string) (*cdpSocket, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("expected local ws:// endpoint, got %q", u.Scheme)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	if _, err = rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nOrigin: http://localhost\r\n\r\n", path, u.Host, key)
	if err != nil {
		conn.Close()
		return nil, err
	}
	rd := bufio.NewReader(conn)
	resp, err := http.ReadResponse(rd, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		conn.Close()
		return nil, fmt.Errorf("CDP websocket handshake: %s", resp.Status)
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.Header.Get("Sec-Websocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		conn.Close()
		return nil, errors.New("invalid CDP handshake accept")
	}
	return &cdpSocket{conn: conn, rd: rd}, nil
}
func (c *cdpSocket) close() { _ = c.conn.Close() }
func (c *cdpSocket) writeFrame(op byte, payload []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	var head [14]byte
	head[0] = 0x80 | op
	n := len(payload)
	pos := 2
	switch {
	case n < 126:
		head[1] = 0x80 | byte(n)
	case n < 65536:
		head[1] = 0x80 | 126
		binary.BigEndian.PutUint16(head[2:], uint16(n))
		pos = 4
	default:
		head[1] = 0x80 | 127
		binary.BigEndian.PutUint64(head[2:], uint64(n))
		pos = 10
	}
	if _, err := rand.Read(head[pos : pos+4]); err != nil {
		return err
	}
	mask := head[pos : pos+4]
	if _, err := c.conn.Write(head[:pos+4]); err != nil {
		return err
	}
	copyBytes := append([]byte(nil), payload...)
	for i := range copyBytes {
		copyBytes[i] ^= mask[i%4]
	}
	_, err := c.conn.Write(copyBytes)
	return err
}
func (c *cdpSocket) readMessage() ([]byte, error) {
	var complete []byte
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(12 * time.Minute)); err != nil {
			return nil, err
		}
		var head [2]byte
		if _, err := io.ReadFull(c.rd, head[:]); err != nil {
			return nil, err
		}
		opcode := head[0] & 0x0f
		fin := head[0]&0x80 != 0
		size := uint64(head[1] & 0x7f)
		if size == 126 {
			var v [2]byte
			if _, err := io.ReadFull(c.rd, v[:]); err != nil {
				return nil, err
			}
			size = uint64(binary.BigEndian.Uint16(v[:]))
		}
		if size == 127 {
			var v [8]byte
			if _, err := io.ReadFull(c.rd, v[:]); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(v[:])
		}
		if size > 80<<20 {
			return nil, fmt.Errorf("CDP payload too large: %d bytes", size)
		}
		var mask [4]byte
		masked := head[1]&0x80 != 0
		if masked {
			if _, err := io.ReadFull(c.rd, mask[:]); err != nil {
				return nil, err
			}
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(c.rd, data); err != nil {
			return nil, err
		}
		if masked {
			for i := range data {
				data[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, data); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1, 0x2, 0x0:
			complete = append(complete, data...)
			if fin {
				return complete, nil
			}
		default:
			return nil, fmt.Errorf("CDP invalid websocket opcode %d", opcode)
		}
	}
}

type cdpReply struct {
	ID    int `json:"id"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Result json.RawMessage `json:"result"`
}

func (c *cdpSocket) call(method string, params any) (json.RawMessage, error) {
	c.next++
	id := c.next
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := c.writeFrame(1, req); err != nil {
		return nil, err
	}
	for {
		raw, err := c.readMessage()
		if err != nil {
			return nil, fmt.Errorf("CDP %s: %w", method, err)
		}
		var r cdpReply
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		if r.ID != id {
			continue
		} // ignore Page/Network/Runtime events
		if r.Error != nil {
			return nil, fmt.Errorf("CDP %s (%d): %s", method, r.Error.Code, r.Error.Message)
		}
		return r.Result, nil
	}
}
func (c *cdpSocket) evaluate(expression string, await bool) (json.RawMessage, error) {
	r, err := c.call("Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": await, "userGesture": true})
	if err != nil {
		return nil, err
	}
	var v struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(r, &v); err != nil {
		return nil, err
	}
	if v.ExceptionDetails != nil {
		msg := v.ExceptionDetails.Exception.Description
		if msg == "" {
			msg = v.ExceptionDetails.Text
		}
		return nil, errors.New(msg)
	}
	if v.Result.Type == "undefined" {
		return json.RawMessage("null"), nil
	}
	if len(v.Result.Value) == 0 {
		return nil, fmt.Errorf("runtime evaluated type %q: %s", v.Result.Type, v.Result.Description)
	}
	return v.Result.Value, nil
}
func waitCDP(port int) (string, error) {
	client := http.Client{Timeout: 2 * time.Second}
	for tries := 0; tries < 100; tries++ {
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json/list")
		if err == nil {
			var pages []struct {
				Type                 string `json:"type"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			err = json.NewDecoder(resp.Body).Decode(&pages)
			resp.Body.Close()
			if err == nil {
				for _, p := range pages {
					if p.Type == "page" && p.WebSocketDebuggerURL != "" {
						return p.WebSocketDebuggerURL, nil
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", errors.New("Chromium did not expose a CDP page within 10 seconds")
}
