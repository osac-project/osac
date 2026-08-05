/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package openapi

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/osac-project/fulfillment-service/internal/cache"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed swagger_ui.html
var swaggerUiHtml []byte

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "openapi [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.args.projectDir,
		"project-dir",
		"",
		projectDirFlagHelp,
	)
	flags.StringVar(
		&runner.args.outputDir,
		"output-dir",
		"",
		outputDirFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		projectDir string
		outputDir  string
	}
	logger                 *slog.Logger
	projectDir             string
	cacheDir               string
	outputDir              string
	tmpDir                 string
	bufPath                string
	curlPath               string
	javaPath               string
	sha256sumPath          string
	protocGenOpenApiV2Path string
	swaggerCodegenCliPath  string
	console                *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context and create a cancellable version:
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Get the cache directory, the logger and the console:
	c.cacheDir = cache.DirFromContext(ctx)
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Check that the project directory is provided:
	if c.args.projectDir == "" {
		return fmt.Errorf("project directory is required")
	}
	c.projectDir, err = filepath.Abs(c.args.projectDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of project directory: %w", err)
	}

	// Check that the project directory exists:
	_, err = os.Stat(c.projectDir)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("project directory '%s' does not exist", c.projectDir)
	}
	if err != nil {
		return fmt.Errorf("failed to check if project directory '%s' exists: %w", c.projectDir, err)
	}

	// Check that the output directory is provided:
	if c.args.outputDir == "" {
		return fmt.Errorf("output directory is required")
	}
	c.outputDir, err = filepath.Abs(c.args.outputDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of output directory: %w", err)
	}

	// Ensure that we have the tools that we need:
	c.bufPath, err = exec.LookPath("buf")
	if err != nil {
		return fmt.Errorf("'buf' not found: %w", err)
	}
	c.curlPath, err = exec.LookPath("curl")
	if err != nil {
		return fmt.Errorf("'curl' not found: %w", err)
	}
	c.javaPath, err = exec.LookPath("java")
	if err != nil {
		return fmt.Errorf("'java' not found: %w", err)
	}
	c.sha256sumPath, err = exec.LookPath("sha256sum")
	if err != nil {
		return fmt.Errorf("'sha256sum' not found: %w", err)
	}

	// Download the 'protoc-gen-openapiv2' binary file:
	c.protocGenOpenApiV2Path, err = c.downloadFile(ctx, downloadFileArgs{
		Url:        protocGenApiV2Url,
		Name:       "protoc-gen-openapiv2",
		Executable: true,
		Sum:        protocGenApiV2Sum,
	})
	if err != nil {
		return err
	}

	// Download the 'swagger-codegen-cli' jar file:
	c.swaggerCodegenCliPath, err = c.downloadFile(ctx, downloadFileArgs{
		Url:  swaggerCodegenCliUrl,
		Name: "swagger-codegen-cli.jar",
		Sum:  swaggerCodegenCliSum,
	})
	if err != nil {
		return err
	}

	// Create a temporary directory for intermediate artifacts:
	c.tmpDir, err = os.MkdirTemp("", "*.build")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(c.tmpDir)

	// Generate the OpenAPI artifacts for the public and private modules:
	publicModuleDir := filepath.Join(c.projectDir, "proto", "public")
	err = c.generateModule(ctx, publicModuleDir)
	if err != nil {
		return err
	}
	privateModuleDir := filepath.Join(c.projectDir, "proto", "private")
	err = c.generateModule(ctx, privateModuleDir)
	if err != nil {
		return err
	}

	// Write the Swagger UI landing page:
	indexFile := filepath.Join(filepath.Dir(c.outputDir), "index.html")
	c.console.Infof(ctx, "Writing Swagger UI to '%s'\n", indexFile)
	err = os.WriteFile(indexFile, swaggerUiHtml, 0644) // #nosec G306 -- generated docs, needs world-read
	if err != nil {
		return fmt.Errorf("failed to write Swagger UI file '%s': %w", indexFile, err)
	}

	return nil
}

func (c *runnerContext) generateModule(ctx context.Context, moduleDir string) error {
	// Create a directory for the artifacts of version 2:
	v2TmpDir := filepath.Join(c.tmpDir, "v2")

	// Generate a 'buf.gen.yaml' file that will be used to generate version 2 of the OpenAPI specification using
	// the gRPC gateway plugin:
	bufGenFile := filepath.Join(c.tmpDir, "buf.gen.yaml")
	bufGenYaml := map[string]any{
		"version": "v2",
		"managed": map[string]any{
			"enabled": true,
			"override": []any{
				map[string]any{
					"file_option": "go_package_prefix",
					"value":       "github.com/osac-project/fulfillment-service/internal/api",
				},
			},
		},
		"inputs": []any{
			map[string]any{
				"directory": moduleDir,
			},
		},
		"plugins": []any{
			map[string]any{
				"local": c.protocGenOpenApiV2Path,
				"out":   v2TmpDir,
				"opt": []string{
					"allow_merge=true",
					"json_names_for_fields=false",
				},
			},
		},
	}
	bufGenBytes, err := yaml.Marshal(bufGenYaml)
	if err != nil {
		return fmt.Errorf("failed to marshal buf configuration file: %w", err)
	}
	err = os.WriteFile(bufGenFile, bufGenBytes, 0600)
	if err != nil {
		return fmt.Errorf("failed to write buf configuration file '%s': %w", bufGenFile, err)
	}

	// Run 'buf generate' to generate version 2 of the OpenAPI specification:
	bufCmd := exec.CommandContext( // #nosec G204 -- binary resolved via exec.LookPath
		ctx,
		c.bufPath,
		"generate",
	)
	bufCmd.Dir = c.tmpDir
	bufCmd.Stdout = c.console
	bufCmd.Stderr = c.console
	err = bufCmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run 'buf generate': %w", err)
	}

	// Find the generated version 2 file:
	v2TmpFiles, err := filepath.Glob(filepath.Join(v2TmpDir, "*.json"))
	if err != nil {
		return err
	}
	if len(v2TmpFiles) != 1 {
		return fmt.Errorf("expected exactly one generated OpenAPI file, but found %d", len(v2TmpFiles))
	}
	v2TmpFile := v2TmpFiles[0]

	// Process the names of schemas for streaming methods:
	err = c.fixStreamingResultSchemaNames(v2TmpFile)
	if err != nil {
		return err
	}

	// Use the 'swagger-codegen-cli' tool to read the generated version 2 and write version 3:
	v3TmpDir := filepath.Join(c.tmpDir, "v3")
	err = os.MkdirAll(v3TmpDir, 0700)
	if err != nil {
		return err
	}
	swaggerCmd := exec.CommandContext( // #nosec G204 -- binary resolved via exec.LookPath
		ctx,
		c.javaPath,
		"-jar", c.swaggerCodegenCliPath,
		"generate",
		"--lang", "openapi-yaml",
		"--input-spec", v2TmpFile,
		"--output", v3TmpDir,
	)
	swaggerCmd.Dir = c.tmpDir
	swaggerCmd.Stdout = c.console
	swaggerCmd.Stderr = c.console
	err = swaggerCmd.Run()
	if err != nil {
		return err
	}

	// Find the generated version 3 file:
	v3TmpFiles, err := filepath.Glob(filepath.Join(v3TmpDir, "*.yaml"))
	if err != nil {
		return err
	}
	if len(v3TmpFiles) != 1 {
		return fmt.Errorf("expected exactly one generated OpenAPI file, but found %d", len(v3TmpFiles))
	}
	v3TmpFile := v3TmpFiles[0]

	// Calculate the base name for the output files:
	moduleName := filepath.Base(moduleDir)

	// Set the spec title and remove the default servers entry:
	err = c.setSpecMetadata(v3TmpFile, moduleName)
	if err != nil {
		return err
	}

	// Write the files to the output directory:
	v2OutDir := filepath.Join(c.outputDir, "v2")
	err = os.MkdirAll(v2OutDir, 0700)
	if err != nil {
		return err
	}
	v2OutFile := filepath.Join(v2OutDir, moduleName+".json")
	c.console.Infof(ctx, "Writing version 2 OpenAPI file to '%s'\n", v2OutFile)
	err = c.copyFile(ctx, v2TmpFile, v2OutFile)
	if err != nil {
		return err
	}
	v3OutDir := filepath.Join(c.outputDir, "v3")
	err = os.MkdirAll(v3OutDir, 0700)
	if err != nil {
		return err
	}
	v3OutFile := filepath.Join(v3OutDir, moduleName+".yaml")
	c.console.Infof(ctx, "Writing version 3 OpenAPI file to '%s'\n", v3OutFile)
	err = c.copyFile(ctx, v3TmpFile, v3OutFile)
	if err != nil {
		return err
	}

	return nil
}

func (r *runnerContext) copyFile(ctx context.Context, src, dst string) error {
	srcStream, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() {
		err := srcStream.Close()
		if err != nil {
			r.logger.WarnContext(
				ctx,
				"Failed to close source file",
				slog.String("file", src),
				slog.Any("error", err),
			)
		}
	}()
	dstStream, err := os.Create(filepath.Clean(dst))
	if err != nil {
		return err
	}
	defer func() {
		err := dstStream.Close()
		if err != nil {
			r.logger.WarnContext(
				ctx,
				"Failed to close destination file",
				slog.String("file", dst),
				slog.Any("error", err),
			)
		}
	}()
	_, err = io.Copy(dstStream, srcStream)
	return err
}

func (r *runnerContext) setSpecMetadata(path, moduleName string) error {
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	var spec map[string]any
	err = yaml.Unmarshal(content, &spec)
	if err != nil {
		return fmt.Errorf("failed to parse OpenAPI spec '%s': %w", path, err)
	}
	info, _ := spec["info"].(map[any]any)
	if info == nil {
		info = make(map[any]any)
		spec["info"] = info
	}
	info["title"] = fmt.Sprintf("OSAC %s API", strings.ToUpper(moduleName[:1])+moduleName[1:])
	removeDefaultServers(spec)
	out, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal OpenAPI spec: %w", err)
	}
	return os.WriteFile(filepath.Clean(path), out, 0644) // #nosec G306 -- generated docs, needs world-read
}

func removeDefaultServers(spec map[string]any) {
	servers, ok := spec["servers"].([]any)
	if !ok || len(servers) != 1 {
		return
	}
	server, ok := servers[0].(map[any]any)
	if !ok {
		return
	}
	if len(server) == 1 {
		if url, _ := server["url"].(string); url == "/" {
			delete(spec, "servers")
		}
	}
}

// fixStreamingResultSchemaNames fixes the names of schemas for streaming methods, replacing 'Stream result of ...'
// with '...StreamResult'. This is necessary because some tools (for example 'oq') do not support schema names with
// with spaces.
func (r *runnerContext) fixStreamingResultSchemaNames(path string) error {
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	text := string(content)
	text = streamResultRe.ReplaceAllString(text, `${1}StreamResult`)
	content = []byte(text)
	return os.WriteFile(filepath.Clean(path), content, 0600) // #nosec G703 -- content taint from ReadFile, not a path traversal
}

// downloadFileArgs contains the arguments for the downloadFile method.
type downloadFileArgs struct {
	// Url is the URL of the file to download.
	Url string

	// Name is the name of the local file to create.
	Name string

	// Executable indicates that the file needs to be executable.
	Executable bool

	// Sum is the checksum of the file to download.
	Sum string
}

// downloadFile downloads a file from the given URL to the cache, and and returns the absolute path of that file.
func (r *runnerContext) downloadFile(ctx context.Context, args downloadFileArgs) (result string, err error) {
	// Check parameters:
	if args.Url == "" {
		err = fmt.Errorf("URL is required")
		return
	}
	if args.Name == "" {
		err = fmt.Errorf("name for URL '%s' is required", args.Url)
		return
	}
	if args.Sum == "" {
		err = fmt.Errorf("checksum for URL '%s' is required", args.Url)
		return
	}

	// Create the cache directory:
	cacheDir := filepath.Join(r.cacheDir, "downloads")
	err = os.MkdirAll(cacheDir, 0700)
	if err != nil {
		err = fmt.Errorf("failed to create cache directory '%s': %w", cacheDir, err)
		return
	}

	// Calculate the absolute path of the cacheFile in the cache:
	cacheFile := filepath.Join(cacheDir, args.Name)
	cacheFile, err = filepath.Abs(cacheFile)
	if err != nil {
		return
	}

	// If the file already exists then we can use it directly, but first we need to verify its checksum. If that
	// fails we remove the file and download it again.
	_, err = os.Stat(cacheFile)
	if err == nil {
		if r.verifyChecksum(ctx, cacheFile, args.Sum) == nil {
			result = cacheFile
			return
		}
		err = os.Remove(cacheFile)
		if err != nil {
			err = fmt.Errorf("failed to remove cached file '%s': %w", cacheFile, err)
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		err = fmt.Errorf("failed to check cached file '%s': %w", cacheFile, err)
		return
	}

	// We use 'curl' to download files because it supports redirection and other details that we do not wish to
	// implement ourselves.
	r.console.Infof(ctx, "Downloading file from '%s' to '%s'\n", args.Url, cacheFile)
	curlCmd := exec.CommandContext( // #nosec G204 -- binary resolved via exec.LookPath
		ctx,
		r.curlPath,
		"--location",
		"--silent",
		"--fail",
		"--output",
		cacheFile,
		args.Url,
	)
	curlCmd.Dir = cacheDir
	curlCmd.Stdout = r.console
	curlCmd.Stderr = r.console
	err = curlCmd.Run()
	if err != nil {
		err = fmt.Errorf("failed to download file from '%s': %w", args.Url, err)
		return
	}

	// Verify the checksum of the file:
	err = r.verifyChecksum(ctx, cacheFile, args.Sum)
	if err != nil {
		err = fmt.Errorf("failed to verify checksum of file '%s': %w", cacheFile, err)
		return
	}

	// Make the file executable:
	if args.Executable {
		err = os.Chmod(cacheFile, 0700) // #nosec G302 -- making downloaded tool executable
		if err != nil {
			err = fmt.Errorf("failed to set executable permission on file '%s': %w", cacheFile, err)
			return
		}
	}

	// Return the resulting cache file:
	result = cacheFile
	return
}

// verifyChecksum verifies the checksum of a file using the 'sha256sum' tool.
func (r *runnerContext) verifyChecksum(ctx context.Context, path, sum string) error {
	sumBuf := &bytes.Buffer{}
	fmt.Fprintf(sumBuf, "%s %s\n", sum, path)
	sumCmd := exec.CommandContext(ctx, r.sha256sumPath, "--check") // #nosec G204 -- binary resolved via exec.LookPath
	sumCmd.Dir = filepath.Dir(path)
	sumCmd.Stdin = sumBuf
	sumCmd.Stdout = r.console
	sumCmd.Stderr = r.console
	return sumCmd.Run()
}

// Version of tools to download:
const (
	protocGenApiV2Version    = "2.29.0"
	swaggerCodegenCliVersion = "3.0.81"
)

// Checksums of the files to download. These have been calculated using the 'sh256sum'. Remember to update this if you
// change the versions of the tools.
const (
	protocGenApiV2Sum    = "804794a445ae57914b58df059cb9cf96a9f2baf25501f0039b6dd5fbca260b0a"
	swaggerCodegenCliSum = "2f75700860868a4f46d64ccf97b5e05dd529fb294e8b38dcb1abbb1f53b3d930"
)

// protocGenApiV2Url is the URL of the 'protoc-gen-openapiv2' plugin:
var protocGenApiV2Url = fmt.Sprintf(
	"https://github.com/grpc-ecosystem/grpc-gateway/releases/download/v%[1]s/protoc-gen-openapiv2-v%[1]s-linux-x86_64",
	protocGenApiV2Version,
)

// swaggerCodegenCliUrl is the URL of the 'swagger-codegen-cli' tool:
var swaggerCodegenCliUrl = fmt.Sprintf(
	"https://repo1.maven.org/maven2/io/swagger/codegen/v3/swagger-codegen-cli/%[1]s/swagger-codegen-cli-%[1]s.jar",
	swaggerCodegenCliVersion,
)

var streamResultRe = regexp.MustCompile(`Stream result of ([^"\':\s]+)`)

const shortHelp = "Generate OpenAPI specification"

const longHelp = `
Generate OpenAPI specification.
`

const projectDirFlagHelp = `
_DIRECTORY_ - Directory where the source code of the project is located.
`

const outputDirFlagHelp = `
_DIRECTORY_ - Directory where the generated _OpenAPI_ specification will be written.
`
