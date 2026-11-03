/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package echo

import (
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// RequestLogger logs one line per HTTP request after the handler returns.
//
// Query values, headers, and bodies stay out of the line: E2E tests poll
// these endpoints, and the log must not repeat what they sent.
//
// status is the code the server sent or tried to send. client_gone is true
// when the response write failed or the request context ended first, which
// means the caller did not get the response. Go cannot tell a disconnected
// caller what status it would have received.
func RequestLogger(logger logr.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			// A handler that writes nothing still gets a 200 from net/http.
			status = http.StatusOK
		}

		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
			"client_gone", recorder.writeErr != nil || r.Context().Err() != nil,
		)
	})
}

// responseRecorder keeps the status code and the first write error of the
// wrapped handler, which a bare http.ResponseWriter does not expose.
type responseRecorder struct {
	http.ResponseWriter
	status   int
	writeErr error
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(data)
	if err != nil {
		r.writeErr = err
	}
	return n, err
}
