package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 50 * time.Second
)

// WSConn bridges a single WebSocket client to a hub subscription. On connect
// the client receives a backfill of every retained message above last_seq,
// then a live stream. Reconnecting with a last_seq cursor resumes gap-free.
type WSConn struct {
	conn    *websocket.Conn
	hub     *hub.Hub
	lastSeq int64
	logger  *slog.Logger
}

// NewWSConn wraps an upgraded connection.
func NewWSConn(conn *websocket.Conn, h *hub.Hub, lastSeq int64, logger *slog.Logger) *WSConn {
	return &WSConn{conn: conn, hub: h, lastSeq: lastSeq, logger: logger}
}

// Serve runs the read and write pumps until the client disconnects, the
// hub closes, or ctx is cancelled.
func (c *WSConn) Serve(ctx context.Context) {
	sub := c.hub.Subscribe(c.lastSeq)
	defer c.hub.Unsubscribe(sub)
	defer func() { _ = c.conn.Close() }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go c.readPump(cancel)
	c.writePump(ctx, sub)
}

// readPump consumes client frames purely to observe pongs and disconnects.
// The dashboard client is receive-only, so any client message is ignored.
func (c *WSConn) readPump(cancel context.CancelFunc) {
	defer cancel()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *WSConn) writePump(ctx context.Context, sub *hub.Subscriber) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.sendClose()
			return
		case msg, ok := <-sub.C():
			if !ok {
				c.sendClose()
				return
			}
			payload, err := hub.Encode(msg)
			if err != nil {
				c.logger.Warn("encode message failed", "error", err)
				continue
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *WSConn) sendClose() {
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutdown"))
}
