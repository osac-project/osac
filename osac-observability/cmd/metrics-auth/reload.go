// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"
)

// fileStamp identifies the version of a mounted file by size and modification
// time. kubelet swaps a renewed Secret atomically, so either field changes when
// the contents do.
type fileStamp struct {
	size    int64
	modTime time.Time
}

func stampOf(info os.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), modTime: info.ModTime()}
}

// reloadingCertificate re-reads the serving key pair on every handshake, so a
// cert-manager renewal reaches new connections without a pod restart. A read
// that fails mid-swap keeps the last good pair: serving it a little longer
// beats failing every handshake until the Secret settles.
type reloadingCertificate struct {
	certFile string
	keyFile  string
	mu       sync.Mutex
	current  *tls.Certificate
}

func newReloadingCertificate(certFile, keyFile string) (*reloadingCertificate, error) {
	certificate := &reloadingCertificate{certFile: certFile, keyFile: keyFile}
	if err := certificate.reload(); err != nil {
		return nil, err
	}
	return certificate, nil
}

func (c *reloadingCertificate) reload() error {
	loaded, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return err
	}
	c.current = &loaded
	return nil
}

// GetCertificate implements tls.Config.GetCertificate.
func (c *reloadingCertificate) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.reload(); err != nil {
		if c.current != nil {
			return c.current, nil
		}
		return nil, fmt.Errorf("loading server certificate: %w", err)
	}
	return c.current, nil
}

// reloadingCertPool re-reads the write CA bundle when the file changes, so a
// rotated CA verifies clients on the next request without a restart. A failed
// read keeps the last good pool rather than rejecting every write.
type reloadingCertPool struct {
	path  string
	mu    sync.Mutex
	stamp fileStamp
	pool  *x509.CertPool
}

func newReloadingCertPool(path string) (*reloadingCertPool, error) {
	pool, err := loadCertPool(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &reloadingCertPool{path: path, stamp: stampOf(info), pool: pool}, nil
}

// Pool returns the current write CA pool, reloading it when the file changed.
func (p *reloadingCertPool) Pool() *x509.CertPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	info, err := os.Stat(p.path)
	if err != nil || stampOf(info) == p.stamp {
		return p.pool
	}
	pool, err := loadCertPool(p.path)
	if err != nil {
		// Keep the previous pool and retry on the next request.
		return p.pool
	}
	p.pool = pool
	p.stamp = stampOf(info)
	return p.pool
}

// serverTLSConfig returns the mTLS listener configuration. The handler verifies
// the client chain, expiry and revocation so every rejection is an HTTP 401
// rather than a handshake failure; the TLS layer still requires that a
// certificate was presented.
func serverTLSConfig(certificate *reloadingCertificate) *tls.Config {
	return &tls.Config{
		GetCertificate: certificate.GetCertificate,
		ClientAuth:     tls.RequireAnyClientCert,
		MinVersion:     tls.VersionTLS12,
	}
}
