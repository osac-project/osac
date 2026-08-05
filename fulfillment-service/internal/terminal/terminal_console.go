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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"gopkg.in/yaml.v3"

	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/rendering"
	"github.com/osac-project/fulfillment-service/internal/templating"
)

// ConsoleBuilder contains the data and logic needed to create a console. Don't create objects of this type directly,
// use the NewConsole function instead.
type ConsoleBuilder struct {
	logger *slog.Logger
	stdout io.Writer
	stderr io.Writer
	helper reflection.Helper
}

// Console is helps writing messages to the console. Don't create objects of this type directly, use the NewConsole
// function instead.
type Console struct {
	logger *slog.Logger
	stdout io.Writer
	stderr io.Writer
	engine *templating.Engine
	helper reflection.Helper
}

// NewConsole creates a builder that can the be used to create a template engine.
func NewConsole() *ConsoleBuilder {
	return &ConsoleBuilder{}
}

// SetLogger sets the logger that the console will use to write messages to the log. This is mandatory.
func (b *ConsoleBuilder) SetLogger(value *slog.Logger) *ConsoleBuilder {
	b.logger = value
	return b
}

// SetStdout sets the writer that the console will use to write messages to the console. This is optional, the default
// is to use os.Stdout and there is usually no need to change it; it is intended for unit tests.
func (b *ConsoleBuilder) SetStdout(value io.Writer) *ConsoleBuilder {
	b.stdout = value
	return b
}

// SetStderr sets the writer that the console will use to write error messages to the console. This is optional, the default
// is to use os.Stderr and there is usually no need to change it; it is intended for unit tests.
func (b *ConsoleBuilder) SetStderr(value io.Writer) *ConsoleBuilder {
	b.stderr = value
	return b
}

// SetHelper sets the reflection helper that will be used to introspect objects. This is optional. If not set then
// functions like 'table' that need reflection will not be available.
func (b *ConsoleBuilder) SetHelper(value reflection.Helper) *ConsoleBuilder {
	b.helper = value
	return b
}

// Build uses the configuration stored in the builder to create a new console.
func (b *ConsoleBuilder) Build() (result *Console, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Set the default writers if needed:
	stdout := b.stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := b.stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// Create the console object first so we can reference its methods when building the template engine:
	console := &Console{
		logger: b.logger,
		stdout: stdout,
		stderr: stderr,
		helper: b.helper,
	}

	// Create the template engine:
	console.engine, err = templating.NewEngine().
		SetLogger(b.logger).
		AddFunction("binary", console.binaryFunc).
		AddFunction("table", console.tableFunc).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create templating engine: %w", err)
		return
	}

	// Return the console object:
	result = console
	return
}

// AddTemplates adds one temlate file system containing templates, including only the templates that are in the given
// directory.
func (c *Console) AddTemplates(fs iofs.FS, dir string) error {
	sub, err := iofs.Sub(fs, dir)
	if err != nil {
		return fmt.Errorf("failed to get templates sub directory '%s': %w", dir, err)
	}
	return c.engine.AddFS(sub)
}

// SetHelper sets the reflection helper that will be used to introspect objects. This is optional. If not set then
// functions like 'table' that need reflection will not be available.
func (c *Console) SetHelper(value reflection.Helper) {
	c.helper = value
}

// Infof writes a message to the standard output stream of the console.
func (c *Console) Infof(ctx context.Context, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	c.logger.DebugContext(
		ctx,
		"Console info",
		slog.String("format", format),
		slog.Any("args", args),
		slog.Any("text", text),
	)
	_, err := c.stdout.Write([]byte(text))
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to write info",
			slog.String("text", text),
			slog.Any("error", err),
		)
	}
}

// Errorf writes a message to the standard error stream of the console.
func (c *Console) Errorf(ctx context.Context, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	c.logger.ErrorContext(
		ctx,
		"Console error",
		slog.String("format", format),
		slog.Any("args", args),
		slog.Any("text", text),
	)
	_, err := c.stderr.Write([]byte(text))
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to write error",
			slog.String("text", text),
			slog.Any("error", err),
		)
	}
}

// Stderr returns the writer used for error output. This is the writer
// configured via SetStderr on the builder, defaulting to os.Stderr.
func (c *Console) Stderr() io.Writer {
	return c.stderr
}

// Render renders the given template with the given data to stdout. The template should be a template file name that
// was added via AddTemplatesFS. If no template file systems have been added, this method will log an error.
func (c *Console) Render(ctx context.Context, template string, data any) {
	var buffer bytes.Buffer
	err := c.engine.Execute(&buffer, template, data)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to execute template",
			slog.String("template", template),
			slog.Any("error", err),
		)
		return
	}
	text := buffer.String()
	lines := strings.Split(text, "\n")
	previousEmpty := true
	for _, line := range lines {
		currentEmpty := len(line) == 0
		if currentEmpty {
			if !previousEmpty {
				_, err := fmt.Fprintf(c.stdout, "\n")
				if err != nil {
					c.logger.ErrorContext(
						ctx,
						"Failed to write empty line",
						slog.Any("error", err),
					)
				}
				previousEmpty = true
			}
		} else {
			_, err := fmt.Fprintf(c.stdout, "%s\n", line)
			if err != nil {
				c.logger.ErrorContext(
					ctx,
					"Failed to write line",
					slog.String("line", line),
					slog.Any("error", err),
				)
			}
			previousEmpty = false
		}
	}
}

// RenderJson renders the given data as JSON to stdout. If the terminal supports color, the output will be colorized
// using the chroma syntax highlighter.
func (c *Console) RenderJson(ctx context.Context, data any) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to encode JSON",
			slog.Any("error", err),
		)
		return
	}
	text := string(bytes) + "\n"
	if err := c.renderColored(ctx, text, "json"); err != nil {
		c.logger.ErrorContext(ctx, "Failed to render JSON", slog.Any("error", err))
	}
}

// RenderYaml renders the given data as YAML to stdout. If the terminal supports color, the output will be colorized
// using the chroma syntax highlighter.
func (c *Console) RenderYaml(ctx context.Context, data any) {
	buffer := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	err := encoder.Encode(data)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to encode YAML",
			slog.Any("error", err),
		)
		return
	}
	encoder.Close()
	if err := c.renderColored(ctx, buffer.String(), "yaml"); err != nil {
		c.logger.ErrorContext(ctx, "Failed to render YAML", slog.Any("error", err))
	}
}

// renderColored renders the given text to stdout with syntax highlighting using the specified lexer. If the terminal
// doesn't support color or an error occurs, it falls back to plain text output.
func (c *Console) renderColored(ctx context.Context, text string, format string) error {
	// If the writer isn't a file then we can't decide if it supports color, so we just print the text:
	file, ok := c.stdout.(*os.File)
	if !ok {
		_, err := c.stdout.Write([]byte(text))
		return err
	}

	// If the file isn't a terminal, then we don't want to use color to not interfere with other tools
	// thayt may want to process the output.
	if !isatty.IsTerminal(file.Fd()) {
		_, err := file.Write([]byte(text))
		return err
	}

	// If we are here then we can use color:
	lexer := lexers.Get(format)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get(colorStyleName)
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get(colorFormatterName)
	if formatter == nil {
		formatter = formatters.Fallback
	}
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"Failed to tokenize text",
			slog.String("format", format),
			slog.Any("error", err),
		)
		_, err := file.Write([]byte(text))
		return err
	}
	return formatter.Format(colorable.NewColorable(file), style, iterator)
}

// Write is an implementation of the io.Write interface that allows the console to be used as a writer if needed.
func (c *Console) Write(p []byte) (n int, err error) {
	n, err = c.stdout.Write(p)
	return
}

// tableFunc is a template function that renders a list of objects as a table. The objects parameter must be a slice
// of objects that implement the proto.Message interface. This will not work and return an error if the reflection
// helper is not set.
func (c *Console) tableFunc(objects any) (result string, err error) {
	if c.helper == nil {
		err = fmt.Errorf("the 'table' function requires the reflection helper, but it isn't set")
		return
	}
	var buffer bytes.Buffer
	renderer, err := rendering.NewTableRenderer().
		SetLogger(c.logger).
		SetHelper(c.helper).
		SetWriter(&buffer).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create table renderer: %w", err)
		return
	}
	err = renderer.Render(context.Background(), objects)
	if err != nil {
		err = fmt.Errorf("failed to render table: %w", err)
		return
	}
	result = buffer.String()
	return
}

// binaryFunc is a template function that returns the name of the binary.
func (c *Console) binaryFunc() string {
	return os.Args[0]
}

// Details of the color style and formatter used by the console.
const (
	colorStyleName     = "friendly"
	colorFormatterName = "terminal256"
)
