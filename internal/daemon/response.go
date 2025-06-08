package daemon

import (
	"encoding/json"
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
