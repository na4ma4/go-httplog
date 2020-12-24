package httplog

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
)

// loggingHandler is the http.Handler implementation for LoggingHandlerTo and its
// friends.
type loggingHandler struct {
	logger  *zap.Logger
	handler http.Handler
}

// ErrUnimplemented is returned when a method is unimplemented.
var ErrUnimplemented = errors.New("unimplemented method")

func (h loggingHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	t := time.Now()
	logger := makeLogger(w)
	url := *req.URL
	h.handler.ServeHTTP(logger, req)
	writeLog(h.logger, req, url, t, logger.Status(), logger.Size())
}

type loggingResponseWriter interface {
	http.ResponseWriter
	http.Flusher
	http.Pusher
	Status() int
	Size() int
}

func makeLogger(w http.ResponseWriter) loggingResponseWriter {
	return &responseLogger{w: w, status: http.StatusOK}
}

// responseLogger is wrapper of http.ResponseWriter that keeps track of its HTTP
// status code and body size.
type responseLogger struct {
	w      http.ResponseWriter
	status int
	size   int
}

func (l *responseLogger) Push(target string, opts *http.PushOptions) error {
	p, ok := l.w.(http.Pusher)
	if !ok {
		return ErrUnimplemented
	}

	return p.Push(target, opts)
}

func (l *responseLogger) Header() http.Header {
	return l.w.Header()
}

func (l *responseLogger) Write(b []byte) (int, error) {
	size, err := l.w.Write(b)
	l.size += size

	if err != nil {
		return size, fmt.Errorf("unable to write: %w", err)
	}

	return size, nil
}

func (l *responseLogger) WriteHeader(s int) {
	l.w.WriteHeader(s)
	l.status = s
}

func (l *responseLogger) Status() int {
	return l.status
}

func (l *responseLogger) Size() int {
	return l.size
}

func (l *responseLogger) Flush() {
	f, ok := l.w.(http.Flusher)
	if ok {
		f.Flush()
	}
}

// writeLog writes a log entry for req to w in Apache Combined Log Format.
// ts is the timestamp with which the entry should be logged.
// status and size are used to provide the response HTTP status and size.
func writeLog(logger *zap.Logger, req *http.Request, url url.URL, ts time.Time, status, size int) {
	// Extract `X-Logging-Username` from request, added by authentication function earlier in process.
	username := "-"
	if req.Header.Get("X-Logging-Username") != "" {
		username = req.Header.Get("X-Logging-Username")
	}

	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}

	uri := req.RequestURI
	if req.ProtoMajor == 2 && req.Method == "CONNECT" {
		uri = req.Host
	}

	if uri == "" {
		uri = url.RequestURI()
	}

	logger.Info(
		"Request",
		zap.Namespace("http"),
		zap.String("host", host),
		zap.String("username", username),
		zap.String("timestamp", ts.Format(time.RFC3339Nano)),
		zap.String("method", req.Method),
		zap.String("uri", uri),
		zap.String("proto", req.Proto),
		zap.Int("status", status),
		zap.Int("size", size),
		zap.String("referer", req.Referer()),
		zap.String("user-agent", req.UserAgent()),
		zap.Duration("request-time", time.Since(ts)),
	)
}

// LoggingHandler return a http.Handler that wraps h and logs requests to out using
// a *zap.Logger.
//
// Example:
//
//  logger, _ := zap.NewProduction()
//  defer logger.Sync() // flushes buffer, if any
//  r := mux.NewRouter()
//  r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
//  	w.Write([]byte("This is a catch-all route"))
//  })
//
//  loggedRouter := httplog.LoggingHandler(logger, r)
//  http.ListenAndServe(":1123", loggedRouter)
//
func LoggingHandler(logger *zap.Logger, h http.Handler) http.Handler {
	return loggingHandler{logger, h}
}
