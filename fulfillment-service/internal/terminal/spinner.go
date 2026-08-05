/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package terminal

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner displays an animated progress indicator that overwrites the current
// terminal line. When the output is not a TTY, it falls back to printing each
// message on its own line.
type Spinner struct {
	writer      io.Writer
	interactive bool
	mu          sync.Mutex
	message     string
	lastPrinted string
	active      bool
	stopCh      chan struct{}
	doneCh      chan struct{}
}

// NewSpinner creates a spinner that writes to the given writer. If the writer
// is a TTY, the spinner animates in place. Otherwise it prints plain lines.
func NewSpinner(writer io.Writer) *Spinner {
	interactive := false
	if f, ok := writer.(*os.File); ok {
		interactive = isatty.IsTerminal(f.Fd())
	}
	return &Spinner{
		writer:      writer,
		interactive: interactive,
	}
}

// Start begins the spinner animation with the given message. If the spinner is
// already running, this is equivalent to Update. In non-interactive mode, the
// message is printed as a plain line.
func (s *Spinner) Start(message string) {
	if !s.interactive {
		s.printPlain(message)
		s.mu.Lock()
		s.active = true
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	if s.active {
		s.message = message
		s.mu.Unlock()
		return
	}
	s.message = message
	s.active = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()

	go s.animate()
}

// Update changes the displayed message without restarting the animation. In
// non-interactive mode, the message is printed only if it differs from the
// last printed message.
func (s *Spinner) Update(message string) {
	if !s.interactive {
		s.printPlain(message)
		return
	}
	s.mu.Lock()
	s.message = message
	s.mu.Unlock()
}

// Stop halts the animation and clears the spinner line. It is safe to call
// when the spinner is not running. The call blocks until the animation
// goroutine has exited.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	s.lastPrinted = ""

	if !s.interactive {
		s.mu.Unlock()
		return
	}

	close(s.stopCh)
	s.mu.Unlock()

	<-s.doneCh
	fmt.Fprint(s.writer, "\r\033[K")
}

// Writer returns the underlying writer that the spinner renders to.
func (s *Spinner) Writer() io.Writer {
	return s.writer
}

func (s *Spinner) animate() {
	defer close(s.doneCh)

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	i := 0
	for {
		s.mu.Lock()
		msg := s.message
		s.mu.Unlock()

		fmt.Fprintf(s.writer, "\r\033[K%s %s", spinnerFrames[i%len(spinnerFrames)], msg)
		i++

		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func (s *Spinner) printPlain(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message != s.lastPrinted {
		fmt.Fprintf(s.writer, "%s\n", message)
		s.lastPrinted = message
	}
}
