/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// CertPoolBuilder contains the data and logic needed to create a certificate pool. Don't create instances of
// this object directly, use the NewCertPoolBuilder function instead.
type CertPoolBuilder struct {
	logger          *slog.Logger
	systemFiles     bool
	kubernetesFiles bool
	root            string
	files           []string
	exts            []string
	certs           []any
}

// NewCertPool creates a builder that can then used to configure and create a certificate pool.
func NewCertPool() *CertPoolBuilder {
	return &CertPoolBuilder{
		exts: certPoolDefaultExts,
	}
}

// SetLogger sets the logger that the loader will use to send messages to the log. This is mandatory.
func (b *CertPoolBuilder) SetLogger(value *slog.Logger) *CertPoolBuilder {
	b.logger = value
	return b
}

// SetRoot sets a custom root directory for resolving file paths. This method is primarily intended for unit tests
// where you need to simulate the presence of files (like Kubernetes certificates or custom CA files) in a controlled
// environment. When a root is set, all file paths (both absolute and relative) will be resolved relative to this
// root directory. This affects both Kubernetes certificate files and any files added via AddFile or AddFiles.
//
// For regular use, there is typically no need to call this method as the default behavior of using paths as-is
// from the filesystem is appropriate for production environments.
func (b *CertPoolBuilder) SetRoot(value string) *CertPoolBuilder {
	b.root = value
	return b
}

// AddSystemFiles adds the system files to the pool. The default is to add them.
func (b *CertPoolBuilder) AddSystemFiles(value bool) *CertPoolBuilder {
	b.systemFiles = value
	return b
}

// AddKubernetesFiles adds the Kubernetes CA files to the pool. The default is to not add them.
func (b *CertPoolBuilder) AddKubernetesFiles(value bool) *CertPoolBuilder {
	b.kubernetesFiles = value
	return b
}

// AddFile adds a file containing CA certificates to be loaded into the pool. The parameter can also be a directory,
// in which case all certificate files within that directory will be loaded. When a directory is specified, the
// loading process is recursive, meaning that all subdirectories will also be processed and their certificate files
// will be included in the pool.
//
// Only files with recognized certificate extensions will be processed when loading from directories. The default
// extensions are .pem, .crt, and .cer, though additional extensions can be configured using AddExtension or
// AddExtensions methods.
func (b *CertPoolBuilder) AddFile(value string) *CertPoolBuilder {
	b.files = append(b.files, value)
	return b
}

// AddFiles adds multiple files containing CA certificates to be loaded into the pool. Each parameter can be either
// a file or a directory. When directories are specified, all certificate files within those directories and their
// subdirectories will be loaded recursively.
//
// The same file extension filtering applies as with AddFile, where only files with recognized certificate extensions
// are processed when loading from directories.
func (b *CertPoolBuilder) AddFiles(values ...string) *CertPoolBuilder {
	b.files = append(b.files, values...)
	return b
}

// AddExtension adds a file name extension that is allowed when loading files from directories. The default is to only
// load files with '.pem', '.crt' and '.cer' extesions.
func (b *CertPoolBuilder) AddExtension(value string) *CertPoolBuilder {
	b.exts = append(b.exts, value)
	return b
}

// AddExtensions adds file name extensions that are allowed when loading files from directories. The default is to only
// load files with '.pem', '.crt' and '.cer' extesions.
func (b *CertPoolBuilder) AddExtensions(values ...string) *CertPoolBuilder {
	b.exts = append(b.exts, values...)
	return b
}

// AddCertificate adds a certificate to the pool. The value can be a string or a slice of bytes containing a PEM
// encoded certificate, or a *x509.Certificate object.
func (b *CertPoolBuilder) AddCertificate(value any) *CertPoolBuilder {
	b.certs = append(b.certs, value)
	return b
}

// AddCertificates adds multiple certificates to the pool. Each value can be a string or a slice of bytes containing
// a PEM encoded certificate, or a *x509.Certificate object.
func (b *CertPoolBuilder) AddCertificates(values ...any) *CertPoolBuilder {
	b.certs = append(b.certs, values...)
	return b
}

// Build uses the data stored in the builder to create a new certificate pool.
func (b *CertPoolBuilder) Build() (result *x509.CertPool, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Start with an empty pool, or with a copy of the system pool if the system files are enabled:
	var pool *x509.CertPool
	if b.systemFiles {
		pool, err = x509.SystemCertPool()
		if err != nil {
			return
		}
	} else {
		pool = x509.NewCertPool()
	}

	// Sort the extensions so that we can use a binary search to check if an extension is allowed:
	sort.Strings(b.exts)

	// Add Kubernetes CA files if enabled:
	if b.kubernetesFiles {
		err = b.loadKubernetesFiles(pool)
		if err != nil {
			return
		}
	}

	// Load configured files:
	err = b.loadConfiguredFiles(pool)
	if err != nil {
		return
	}

	// Load configured certificates:
	for _, cert := range b.certs {
		switch cert := cert.(type) {
		case string:
			ok := pool.AppendCertsFromPEM([]byte(cert))
			if !ok {
				err = fmt.Errorf("failed to add certificate to pool: %w", err)
				return
			}
		case []byte:
			ok := pool.AppendCertsFromPEM(cert)
			if !ok {
				err = fmt.Errorf("failed to add certificate to pool: %w", err)
				return
			}
		case *x509.Certificate:
			pool.AddCert(cert)
		default:
			err = fmt.Errorf(
				"invalid certificate type '%T', should be 'string', '[]byte' or '*x509.Certificate'",
				cert,
			)
			return
		}
	}

	result = pool
	return
}

// resolvePath resolves a file path using the custom root directory if set. If no root is set, returns the original
// path unchanged.
func (b *CertPoolBuilder) resolvePath(path string) string {
	if b.root == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return filepath.Join(b.root, path[1:])
	}
	return filepath.Join(b.root, path)
}

func (b *CertPoolBuilder) loadKubernetesFiles(pool *x509.CertPool) error {
	for _, caFile := range certPoolKubernetesCaFiles {
		resolvedPath := b.resolvePath(caFile)
		err := b.loadFile(pool, resolvedPath)
		if errors.Is(err, os.ErrNotExist) {
			b.logger.Info(
				"Kubernetes CA file doesn't exist",
				slog.String("file", caFile),
			)
			err = nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *CertPoolBuilder) loadConfiguredFiles(pool *x509.CertPool) error {
	for _, caFile := range b.files {
		resolvedPath := b.resolvePath(caFile)
		err := b.loadFile(pool, resolvedPath)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *CertPoolBuilder) loadFile(pool *x509.CertPool, caFile string) error {
	info, err := os.Stat(caFile)
	if err != nil {
		return err
	}
	if info.IsDir() {
		b.logger.Info(
			"Loading CA directory",
			slog.String("directory", caFile),
		)
		dirEntries, err := os.ReadDir(caFile)
		if err != nil {
			return err
		}
		for _, dirEntry := range dirEntries {
			fileName := dirEntry.Name()
			fullPath := filepath.Join(caFile, fileName)
			if dirEntry.IsDir() {
				err := b.loadFile(pool, fullPath)
				if err != nil {
					return err
				}
				continue
			}
			fileExt := filepath.Ext(fileName)
			if !b.validExt(fileExt) {
				b.logger.Info(
					"Ignoring file because it doesn't have a valid extension",
					slog.String("directory", caFile),
					slog.String("file", fileName),
					slog.String("ext", fileExt),
					slog.Any("valid", b.exts),
				)
				continue
			}
			err := b.loadFile(pool, fullPath)
			if err != nil {
				return err
			}
		}
	} else {
		b.logger.Info(
			"Loading CA file",
			slog.String("file", caFile),
		)
		data, err := os.ReadFile(filepath.Clean(caFile))
		if err != nil {
			return err
		}
		ok := pool.AppendCertsFromPEM(data)
		if !ok {
			return fmt.Errorf("file exists, but it '%s' doesn't contain any CA certificate", caFile)
		}
	}
	return nil
}

func (b *CertPoolBuilder) validExt(ext string) bool {
	_, found := slices.BinarySearch(b.exts, ext)
	return found
}

// certPoolDefaultExts is the default list of file name extensions that are allowed when loading files from directories.
var certPoolDefaultExts = []string{
	".cer",
	".crt",
	".pem",
}

// certPoolLoaderKubernetesCaFiles is a list of Kubernetes CA files that will be automatically loaded if they exist.
var certPoolKubernetesCaFiles = []string{
	// This is the CA used for Kubernetes to sign the certificates of service accounts.
	"/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",

	// This is the CA used by OpenShift to sign the certificates generated for services.
	"/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt",
}
