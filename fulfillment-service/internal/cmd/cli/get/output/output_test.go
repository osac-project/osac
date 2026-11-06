/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// makeConsole creates a console that writes to the given writer. It fails the test if the console can't be created.
func makeConsole(t *testing.T, stdout io.Writer) *terminal.Console {
	t.Helper()
	console, err := terminal.NewConsole().
		SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		SetStdout(stdout).
		Build()
	if err != nil {
		t.Fatalf("failed to create the console: %v", err)
	}
	return console
}

// makeCluster creates a cluster message with a stable rendering.
func makeCluster() *publicv1.Cluster {
	return publicv1.Cluster_builder{
		Id: "cluster-1",
		Metadata: publicv1.Metadata_builder{
			Name: "my-cluster",
		}.Build(),
	}.Build()
}

// failingWriter is an io.Writer that fails every write with a persistent error, to simulate a broken standard
// output stream.
type failingWriter struct {
	err error
}

// Write is the implementation of the io.Writer interface.
func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestRender(t *testing.T) {
	ctx := context.Background()

	t.Run("JSON success", func(t *testing.T) {
		var stdout bytes.Buffer
		console := makeConsole(t, &stdout)
		err := Render(ctx, console, Json, "cluster", makeCluster())
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := "{\n  \"id\": \"cluster-1\",\n  \"metadata\": {\n    \"name\": \"my-cluster\"\n  }\n}\n"
		if stdout.String() != expected {
			t.Errorf("expected output %q, got %q", expected, stdout.String())
		}
	})

	t.Run("YAML success", func(t *testing.T) {
		var stdout bytes.Buffer
		console := makeConsole(t, &stdout)
		err := Render(ctx, console, Yaml, "cluster", makeCluster())
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := "id: cluster-1\nmetadata:\n  name: my-cluster\n"
		if stdout.String() != expected {
			t.Errorf("expected output %q, got %q", expected, stdout.String())
		}
	})

	t.Run("JSON write failure", func(t *testing.T) {
		sentinel := errors.New("broken stdout")
		console := makeConsole(t, &failingWriter{err: sentinel})
		err := Render(ctx, console, Json, "cluster", makeCluster())
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("expected the error to wrap %v, got %v", sentinel, err)
		}
		if !strings.Contains(err.Error(), "failed to render cluster") {
			t.Errorf("expected the error to mention the label, got %q", err.Error())
		}
	})

	t.Run("YAML write failure", func(t *testing.T) {
		sentinel := errors.New("broken stdout")
		console := makeConsole(t, &failingWriter{err: sentinel})
		err := Render(ctx, console, Yaml, "cluster", makeCluster())
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("expected the error to wrap %v, got %v", sentinel, err)
		}
		if !strings.Contains(err.Error(), "failed to render cluster") {
			t.Errorf("expected the error to mention the label, got %q", err.Error())
		}
	})
}
