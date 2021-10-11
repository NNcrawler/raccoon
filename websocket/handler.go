package websocket

import (
	"fmt"
	"net/http"
	"raccoon/logger"
	"raccoon/metrics"
	"raccoon/websocket/connection"
	"time"

	pb "raccoon/websocket/proto"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
)

type Handler struct {
	upgrader      *connection.Upgrader
	bufferChannel chan EventsBatch
	PingChannel   chan connection.Conn
}
type EventsBatch struct {
	ConnIdentifier connection.Identifier
	EventReq      *pb.EventRequest
	TimeConsumed  time.Time
	TimePushed    time.Time
}

func PingHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("pong"))
}

//HandlerWSEvents handles the upgrade and the events sent by the peers
func (h *Handler) HandlerWSEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r)
	if err != nil {
		logger.Debugf("[websocket.Handler] %v", err)
		return
	}
	defer conn.Close()
	h.PingChannel <- conn

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
				websocket.CloseAbnormalClosure) {
				logger.Error(fmt.Sprintf("[websocket.Handler] %s closed abruptly: %v", conn.Identifier, err))
				metrics.Increment("batches_read_total", fmt.Sprintf("status=failed,reason=closeerror,conn_group=%s", conn.Identifier.Group))
				break
			}

			metrics.Increment("batches_read_total", fmt.Sprintf("status=failed,reason=unknown,conn_group=%s", conn.Identifier.Group))
			logger.Error(fmt.Sprintf("[websocket.Handler] reading message failed. Unknown failure for %s: %v", conn.Identifier, err)) //no connection issue here
			break
		}
		timeConsumed := time.Now()
		metrics.Count("events_rx_bytes_total", len(message), fmt.Sprintf("conn_group=%s", conn.Identifier.Group))
		payload := &pb.EventRequest{}
		err = proto.Unmarshal(message, payload)
		if err != nil {
			logger.Error(fmt.Sprintf("[websocket.Handler] reading message failed for %s: %v", conn.Identifier, err))
			metrics.Increment("batches_read_total", fmt.Sprintf("status=failed,reason=serde,conn_group=%s", conn.Identifier.Group))
			badrequest := createBadrequestResponse(err)
			conn.WriteMessage(websocket.BinaryMessage, badrequest)
			continue
		}
		metrics.Increment("batches_read_total", fmt.Sprintf("status=success,conn_group=%s", conn.Identifier.Group))
		metrics.Count("events_rx_total", len(payload.Events), fmt.Sprintf("conn_group=%s", conn.Identifier.Group))

		h.bufferChannel <- EventsBatch{
			ConnIdentifier: conn.Identifier,
			EventReq:      payload,
			TimeConsumed:  timeConsumed,
			TimePushed:    (time.Now()),
		}

		resp := createSuccessResponse(payload.ReqGuid)
		success, _ := proto.Marshal(resp)
		conn.WriteMessage(websocket.BinaryMessage, success)
	}
}
