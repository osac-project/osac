/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package echo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// logCapture collects the JSON lines that funcr writes, so a test can assert
// on every field the logger emitted. It is safe for concurrent use because
// the server logs from its own goroutine.
type logCapture struct {
	mutex sync.Mutex
	lines []map[string]any
}

func newLogCapture() (*logCapture, logr.Logger) {
	capture := &logCapture{}
	logger := funcr.NewJSON(func(object string) {
		var line map[string]any
		if err := json.Unmarshal([]byte(object), &line); err != nil {
			panic("log line is not JSON: " + err.Error())
		}
		capture.mutex.Lock()
		defer capture.mutex.Unlock()
		capture.lines = append(capture.lines, line)
	}, funcr.Options{})
	return capture, logger
}

func (c *logCapture) entries() []map[string]any {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]map[string]any(nil), c.lines...)
}

// newLoggedMux serves the same routes as the echo-adapter binary, behind the
// request logger.
func newLoggedMux(logger logr.Logger, store *EventStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", store.HandleEvents)
	mux.HandleFunc("DELETE /events", store.HandleDeleteEvents)
	mux.HandleFunc("GET /events/count", store.HandleCount)
	mux.HandleFunc("GET /events/{id}", store.HandleEventByID)
	return RequestLogger(logger, mux)
}

var _ = Describe("RequestLogger", func() {
	var (
		capture *logCapture
		logger  logr.Logger
		store   *EventStore
	)

	BeforeEach(func() {
		capture, logger = newLogCapture()
		store = NewEventStore(10)
	})

	It("logs method, path, status, duration, and peer address once", func() {
		server := httptest.NewServer(newLoggedMux(logger, store))
		defer server.Close()

		response, err := http.Get(server.URL + "/events/count")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())

		entries := capture.entries()
		Expect(entries).To(HaveLen(1))

		entry := entries[0]
		Expect(entry["msg"]).To(Equal("http request"))
		Expect(entry["level"]).To(BeNumerically("==", 0))
		Expect(entry["method"]).To(Equal(http.MethodGet))
		Expect(entry["path"]).To(Equal("/events/count"))
		Expect(entry["status"]).To(BeNumerically("==", http.StatusOK))
		Expect(entry["duration_ms"]).To(BeNumerically(">=", 0))
		Expect(entry["remote_addr"]).To(HavePrefix("127.0.0.1:"))
		Expect(entry["client_gone"]).To(BeFalse())
	})

	It("logs the path without the query values", func() {
		server := httptest.NewServer(newLoggedMux(logger, store))
		defer server.Close()

		response, err := http.Get(server.URL + "/events?type=osac.resource.created.v1&token=not-for-logs")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())

		entries := capture.entries()
		Expect(entries).To(HaveLen(1))
		Expect(entries[0]["path"]).To(Equal("/events"))

		raw, err := json.Marshal(entries[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("not-for-logs"))
		Expect(string(raw)).NotTo(ContainSubstring("osac.resource.created.v1"))
	})

	It("logs the status the mux returns for an unknown path", func() {
		server := httptest.NewServer(newLoggedMux(logger, store))
		defer server.Close()

		response, err := http.Get(server.URL + "/nope")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())

		entries := capture.entries()
		Expect(entries).To(HaveLen(1))
		Expect(entries[0]["status"]).To(BeNumerically("==", http.StatusNotFound))
		Expect(entries[0]["path"]).To(Equal("/nope"))
	})

	It("logs the handler status, not just 200", func() {
		server := httptest.NewServer(newLoggedMux(logger, store))
		defer server.Close()

		request, err := http.NewRequest(http.MethodDelete, server.URL+"/events", nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())

		entries := capture.entries()
		Expect(entries).To(HaveLen(1))
		Expect(entries[0]["status"]).To(BeNumerically("==", http.StatusNoContent))
	})

	It("logs one line per request", func() {
		server := httptest.NewServer(newLoggedMux(logger, store))
		defer server.Close()

		for range 3 {
			response, err := http.Get(server.URL + "/events/count")
			Expect(err).NotTo(HaveOccurred())
			Expect(response.Body.Close()).To(Succeed())
		}

		Expect(capture.entries()).To(HaveLen(3))
	})

	It("marks a request whose client left before the response", func() {
		arrived := make(chan struct{})
		blocked := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(arrived)
			<-r.Context().Done()
		})
		server := httptest.NewServer(RequestLogger(logger, blocked))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events/count", nil)
		Expect(err).NotTo(HaveOccurred())

		clientDone := make(chan struct{})
		go func() {
			defer close(clientDone)
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
		}()

		Eventually(arrived).Should(BeClosed())
		cancel()
		Eventually(clientDone).Should(BeClosed())

		Eventually(func() int { return len(capture.entries()) }).Should(Equal(1))
		entry := capture.entries()[0]
		Expect(entry["client_gone"]).To(BeTrue())
		Expect(entry["method"]).To(Equal(http.MethodGet))
		Expect(entry["path"]).To(Equal("/events/count"))
		Expect(entry["duration_ms"]).To(BeNumerically(">=", 0))
		// The handler wrote nothing, so the server had no status to send.
		Expect(entry["status"]).To(BeNumerically("==", http.StatusOK))
	})

	It("keeps the log line free of request headers", func() {
		server := httptest.NewServer(newLoggedMux(logger, store))
		defer server.Close()

		request, err := http.NewRequest(http.MethodGet, server.URL+"/events/count", nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer not-for-logs")
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())

		entries := capture.entries()
		Expect(entries).To(HaveLen(1))

		raw, err := json.Marshal(entries[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.ToLower(string(raw))).NotTo(ContainSubstring("bearer"))
	})
})
