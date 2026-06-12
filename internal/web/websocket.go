package web

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type WSEvent struct {
	Type string `json:"type"`
	Time int64  `json:"time,omitempty"`
	Data any    `json:"data,omitempty"`
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: map[*wsClient]struct{}{}}
}

func (h *Hub) Broadcast(event WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			client.close()
		}
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request, hello WSEvent) {
	conn, rw, err := upgradeWebSocket(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client := &wsClient{
		conn: conn,
		rw:   rw,
		send: make(chan []byte, 64),
		done: make(chan struct{}),
	}
	h.add(client)
	defer h.remove(client)

	if data, err := json.Marshal(hello); err == nil {
		client.send <- data
	}

	go client.readLoop()
	client.writeLoop()
}

func (h *Hub) add(client *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = struct{}{}
}

func (h *Hub) remove(client *wsClient) {
	client.close()
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, client)
}

type wsClient struct {
	conn net.Conn
	rw   *bufio.ReadWriter
	send chan []byte
	done chan struct{}
	once sync.Once
}

func (c *wsClient) readLoop() {
	for {
		if _, err := readFrame(c.rw.Reader); err != nil {
			c.close()
			return
		}
	}
}

func (c *wsClient) writeLoop() {
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.done:
			return
		case data := <-c.send:
			if err := writeTextFrame(c.rw, data); err != nil {
				c.close()
				return
			}
		case <-heartbeat.C:
			data, _ := json.Marshal(WSEvent{Type: "heartbeat", Time: time.Now().UnixMilli()})
			if err := writeTextFrame(c.rw, data); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *wsClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, nil, errors.New("missing websocket upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, nil, errors.New("missing Sec-WebSocket-Key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("websocket hijacking unsupported")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	accept := websocketAccept(key)
	_, err = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func writeTextFrame(rw *bufio.ReadWriter, payload []byte) error {
	header := []byte{0x81}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 0xffff:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(payload)))
		header = append(header, size[:]...)
	}
	if _, err := rw.Write(header); err != nil {
		return err
	}
	if _, err := rw.Write(payload); err != nil {
		return err
	}
	return rw.Flush()
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	second, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	opcode := first & 0x0f
	masked := second&0x80 != 0
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var raw [2]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(raw[:]))
	case 127:
		var raw [8]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(raw[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return nil, err
		}
	}
	if length > 1<<20 {
		return nil, errors.New("websocket frame too large")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	if opcode == 0x8 {
		return nil, io.EOF
	}
	return payload, nil
}
