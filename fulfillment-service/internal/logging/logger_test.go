/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Logger", func() {
	It("Rejects unknown level", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("junk").
			Build()
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("level"))
		Expect(msg).To(ContainSubstring("junk"))
		Expect(msg).To(ContainSubstring("unknown"))
		Expect(logger).To(BeNil())
	})

	It("Writes time in UTC", func() {
		// Create a logger that writes to a memory buffer:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("")

		// Verify the fields:
		var msg struct {
			Time string `json:"time"`
		}
		err = json.Unmarshal(buffer.Bytes(), &msg)
		Expect(err).ToNot(HaveOccurred())
		ts, err := time.Parse(time.RFC3339, msg.Time)
		Expect(err).ToNot(HaveOccurred())
		zone, offset := ts.Zone()
		Expect(zone).To(Equal("UTC"))
		Expect(offset).To(BeZero())
	})

	It("Writes time in RFC3339 format", func() {
		// Create a logger that writes to a memory buffer:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("")

		// Verify the fields:
		var msg struct {
			Time string `json:"time"`
		}
		err = json.Unmarshal(buffer.Bytes(), &msg)
		Expect(err).ToNot(HaveOccurred())
		_, err = time.Parse(time.RFC3339, msg.Time)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Writes `ERROR` for error messages", func() {
		// Create a logger that writes to a memory buffer:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Error("")

		// Verify the fields:
		var msg struct {
			Level string `json:"level"`
		}
		err = json.Unmarshal(buffer.Bytes(), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Level).To(Equal("ERROR"))
	})

	It("Writes `INFO` for info messages", func() {
		// Create a logger that writes to a memory buffer:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("")

		// Verify the fields:
		var msg struct {
			Level string `json:"level"`
		}
		err = json.Unmarshal(buffer.Bytes(), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Level).To(Equal("INFO"))
	})

	It("Doesn't write debug messages by default", func() {
		// Create a logger that writes to a memory buffer:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Debug("")

		// Verify that no message was written:
		Expect(buffer.Len()).To(BeZero())
	})

	It("Doesn't write debug messages when level is explicitly set to disable them", func() {
		// Create a logger that writes to a memory buffer:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("info").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Debug("")

		// Verify that no message was written:
		Expect(buffer.Len()).To(BeZero())
	})

	It("Doesn't write to the default file if a writer is provided", func() {
		// We skip this test in non Linux operating systems because there we can't use a
		// temporary directory via the `XDG_CACHE_HOME` environment variable.
		if runtime.GOOS != "linux" {
			Skip("Don't know how to use temporary directory.")
		}

		// Create a temporary cache directory:
		tmpCache, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := os.RemoveAll(tmpCache)
			Expect(err).ToNot(HaveOccurred())
		}()
		oldCache := os.Getenv("XDG_CACHE_HOME")
		defer os.Setenv("XDG_CACHE_HOME", oldCache)
		os.Setenv("XDG_CACHE_HOME", tmpCache)

		// Create the logger with a writer:
		logger, err := NewLogger().
			SetWriter(io.Discard).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the log file hasn't been created:
		file := filepath.Join(tmpCache, "my", "my.log")
		Expect(file).ToNot(BeAnExistingFile())
	})

	It("Writes to the explicitly provided file", func() {
		// Create a temporary directory for the log file:
		tmp, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := os.RemoveAll(tmp)
			Expect(err).ToNot(HaveOccurred())
		}()
		file := filepath.Join(tmp, "my.log")

		// Create the logger:
		logger, err := NewLogger().
			SetLevel("debug").
			SetFile(file).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the file has been created:
		info, err := os.Stat(file)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Size()).To(BeNumerically(">=", 0))

		// Check that the file has read and write permissions for the owner:
		Expect(info.Mode() & 0400).ToNot(BeZero())
		Expect(info.Mode() & 0200).ToNot(BeZero())

		// Check that the file doesn't have execution permissions:
		Expect(info.Mode() & 0111).To(BeZero())
	})

	It("Adds custom field", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			AddField("my-field", "my-value").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField string `json:"my-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
	})

	It("Adds pid field", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			AddField("pid", "%p").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the pid field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Pid int `json:"pid"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Pid).To(Equal(os.Getpid()))
	})

	It("Honors log file flags", func() {
		// Create a temporary directory for the log file:
		tmp, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := os.RemoveAll(tmp)
			Expect(err).ToNot(HaveOccurred())
		}()
		file := filepath.Join(tmp, "my.log")

		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err = flags.Parse([]string{
			"--log-level", "info",
			"--log-file", file,
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		logger, err := NewLogger().
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write two messages, one that should be written and another that should be ignored
		// because of the level configured:
		logger.Info("good message")
		logger.Debug("bad message")

		// Check that the file has been created:
		info, err := os.Stat(file)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Size()).To(BeNumerically(">=", 0))

		// Check that the good message has been written and the bad message hasn't:
		data, err := os.ReadFile(file)
		Expect(err).ToNot(HaveOccurred())
		text := string(data)
		Expect(text).To(ContainSubstring("good message"))
		Expect(text).ToNot(ContainSubstring("bad message"))
	})

	It("Honors one field flag", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-field", "my-field=my-value",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField string `json:"my-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
	})

	It("Honors multiple field flags", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-field", "my-field=my-value",
			"--log-field", "your-field=your-value",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField   string `json:"my-field"`
			YourField string `json:"your-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
		Expect(msg.YourField).To(Equal("your-value"))
	})

	It("Honors one fields flag", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-fields", "my-field=my-value,your-field=your-value",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField   string `json:"my-field"`
			YourField string `json:"your-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
		Expect(msg.YourField).To(Equal("your-value"))
	})

	It("Honors multiple fields flag", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-fields", "my-field=my-value,your-field=your-value",
			"--log-fields", "our-field=our-value,their-field=their-value",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField    string `json:"my-field"`
			YourField  string `json:"your-field"`
			OurField   string `json:"our-field"`
			TheirField string `json:"their-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
		Expect(msg.YourField).To(Equal("your-value"))
		Expect(msg.OurField).To(Equal("our-value"))
		Expect(msg.TheirField).To(Equal("their-value"))
	})

	It("Honors mixed field and fields flag", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-field", "my-field=my-value",
			"--log-fields", "your-field=your-value",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField   string `json:"my-field"`
			YourField string `json:"your-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
		Expect(msg.YourField).To(Equal("your-value"))
	})

	It("Allows comma inside field flag", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-field", "my-field=my-value,your-value",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField string `json:"my-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value,your-value"))
	})

	It("Allows %p inside field", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-field", "%p",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Pid int `json:"pid"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Pid).To(Equal(os.Getpid()))
	})

	It("Allows %p inside field value", func() {
		// Prepare the flags:
		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		AddFlags(flags)
		err := flags.Parse([]string{
			"--log-field", "my-pid=%p",
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetFlags(flags).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info("my message")

		// Check that the custom field has been added:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Pid int `json:"my-pid"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Pid).To(Equal(os.Getpid()))
	})

	It("Redacts sensitive fields by default", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info(
			"my message",
			"!my-field", "my-value",
		)

		// Check that the field has been redacted:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField string `json:"my-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("***"))
	})

	It("Doesn't redact sensitive fields if disabled", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetRedact(false).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info(
			"my message",
			"!my-field", "my-value",
		)

		// Check that the field has been redacted:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField string `json:"my-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("my-value"))
	})

	It("Logger with attributes redacts sensitive fields like parent", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())
		logger = logger.With("!my-field", "my-value")

		// Write a message:
		logger.Info(
			"your message",
			"!your-field", "your-value",
		)

		// Check that the field has been redacted:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyField   string `json:"my-field"`
			YourField string `json:"your-field"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyField).To(Equal("***"))
		Expect(msg.YourField).To(Equal("***"))
	})

	It("Logger with group redacts sensitive fields like parent", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())
		logger = logger.WithGroup("my-group")

		// Write a message:
		logger.Info(
			"my message",
			"!my-field", "my-value",
		)

		// Check that the field has been redacted:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			MyGroup struct {
				MyField string `json:"my-field"`
			} `json:"my-group"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.MyGroup.MyField).To(Equal("***"))
	})

	It("Replaces error with the error message", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info(
			"my message",
			slog.Any("error", errors.New("my error")),
		)

		// Check that error has been replaced:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Error errorDump `json:"error"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Error.Message).To(Equal("my error"))
	})

	It("Adds error goroutine identifier", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info(
			"my message",
			slog.Any("error", errors.New("my error")),
		)

		// Check that error has been replaced:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Error errorDump `json:"error"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Error.Goroutine).To(BeNumerically(">=", 0))
	})

	It("Adds error stack", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		logger.Info(
			"my message",
			slog.Any("error", errors.New("my error")),
		)

		// Check that error has been replaced:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Error errorDump `json:"error"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Error.Stack).ToNot(BeEmpty())
	})

	It("Writes protocol buffers messages with type", func() {
		// Create the logger:
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Write a message:
		object := wrapperspb.Int32(42)
		logger.Info(
			"my message",
			slog.Any("object", object),
		)

		// Check that object has been replaced with the corresponding JSON document, including the type:
		lines := strings.Split(buffer.String(), "\n")
		Expect(lines).To(HaveLen(2))
		var msg struct {
			Object any `json:"object"`
		}
		err = json.Unmarshal([]byte(lines[0]), &msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(msg.Object).To(Equal(map[string]any{
			"@type": "type.googleapis.com/google.protobuf.Int32Value",
			"value": 42.0,
		}))
	})
})
