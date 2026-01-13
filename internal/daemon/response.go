package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
)

type Response struct {
	Messages []ResponseMessage `json:"messages"`
	Data     interface{}       `json:"data,omitempty"`
}

type ResponseMessage struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

// StreamingResponse wraps a writer for streaming individual messages
type StreamingResponse struct {
	w io.Writer
}

// NewStreamingResponse creates a new streaming response writer
func NewStreamingResponse(w io.Writer) *StreamingResponse {
	return &StreamingResponse{w: w}
}

// WriteMessage writes a single message to the stream as a JSON line
func (sr *StreamingResponse) WriteMessage(message, status string) error {
	msg := ResponseMessage{Message: message, Status: status}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = sr.w.Write(append(data, '\n'))
	return err
}

func (r *Response) AddMessage(message string, status string) {
	r.Messages = append(r.Messages, ResponseMessage{
		Message: message,
		Status:  status,
	})
}

func (r *Response) AddData(data interface{}) {
	r.Data = data
}

func (r *Response) ToJSON() string {
	bytes, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

func (r *Response) LogMessages() {
	for _, message := range r.Messages {
		switch message.Status {
		case "INFO":
			slog.Info(message.Message)
		case "WARN":
			slog.Warn(message.Message)
		case "ERROR":
			slog.Error(message.Message)
		default:
			slog.Info(message.Message)
		}
	}
}
