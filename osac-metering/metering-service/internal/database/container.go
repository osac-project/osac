/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Container manages a PostgreSQL instance for integration tests. OpenShell
// runs a local server; other environments use podman/docker. This lives in
// internal/database because test files in other packages import it.
type Container struct {
	logger        logr.Logger
	tool          string
	initdbTool    string
	local         bool
	dataDir       string
	id            string
	host          string
	port          string
	adminPassword string
	userPassword  string
	configFile    string
	adminConn     *pgx.Conn
	runCmd        *exec.Cmd
	lock          sync.Mutex
	count         int
	instances     []*Instance
}

func NewContainer(logger logr.Logger) (*Container, error) {
	if os.Getenv("OPENSHELL_SANDBOX") != "" {
		postgres, err := exec.LookPath("postgres")
		if err != nil {
			return nil, fmt.Errorf("local PostgreSQL server is not installed: %w", err)
		}
		initdb, err := exec.LookPath("initdb")
		if err != nil {
			return nil, fmt.Errorf("local PostgreSQL initialization tool is not installed: %w", err)
		}
		logger.Info("selected sandbox-local PostgreSQL test server", "tool", postgres)
		return &Container{
			logger:        logger,
			tool:          postgres,
			initdbTool:    initdb,
			local:         true,
			adminPassword: randomHex(),
			userPassword:  randomHex(),
		}, nil
	}

	tool, err := selectTool()
	if err != nil {
		return nil, fmt.Errorf("selecting container tool: %w", err)
	}
	logger.Info("selected container tool", "tool", tool)

	return &Container{
		logger:        logger,
		tool:          tool,
		adminPassword: randomHex(),
		userPassword:  randomHex(),
	}, nil
}

func (c *Container) Start(ctx context.Context) error {
	var err error
	if c.local {
		if err := c.startLocal(ctx); err != nil {
			return err
		}
	} else {
		configFile, err := createConfigFile()
		if err != nil {
			return err
		}
		c.configFile = configFile

		c.id = fmt.Sprintf("osac-metering-db-%08x", rand.Uint32())
		c.runCmd = exec.Command(
			c.tool,
			"run",
			"--name", c.id,
			"--env", fmt.Sprintf("POSTGRESQL_ADMIN_PASSWORD=%s", c.adminPassword),
			"--publish", "5432",
			"--rm",
			"--volume", fmt.Sprintf("%s:%s:Z", c.configFile, containerConfigPath),
			containerImage,
		)
		c.runCmd.Stdout = os.Stdout
		c.runCmd.Stderr = os.Stderr
		if err := c.runCmd.Start(); err != nil {
			return fmt.Errorf("starting database container: %w", err)
		}

		if err := c.waitForPort(ctx); err != nil {
			return err
		}
	}
	if err := c.waitForConnection(ctx); err != nil {
		return err
	}

	_, err = c.adminConn.Exec(ctx,
		fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", containerUser, c.userPassword))
	if err != nil {
		return fmt.Errorf("creating shared user: %w", err)
	}

	templateDB := "metering_template"
	_, err = c.adminConn.Exec(ctx,
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", templateDB, containerUser))
	if err != nil {
		return fmt.Errorf("creating template database: %w", err)
	}

	templateURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		containerUser, c.userPassword, c.host, c.port, templateDB,
	)
	templatePool, err := pgxpool.New(ctx, templateURL)
	if err != nil {
		return fmt.Errorf("creating template database pool: %w", err)
	}
	if err := InitializeSchema(ctx, templatePool); err != nil {
		templatePool.Close()
		return fmt.Errorf("initializing template database schema: %w", err)
	}
	templatePool.Close()

	_, err = c.adminConn.Exec(ctx,
		fmt.Sprintf("ALTER DATABASE %s IS_TEMPLATE true", templateDB))
	if err != nil {
		return fmt.Errorf("marking template database: %w", err)
	}

	return nil
}

func (c *Container) startLocal(ctx context.Context) (resultErr error) {
	rootDir, err := os.MkdirTemp("", "osac-metering-postgres-*")
	if err != nil {
		return fmt.Errorf("creating local PostgreSQL data directory: %w", err)
	}
	c.dataDir = rootDir
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(rootDir))
			c.dataDir = ""
		}
	}()
	dataDir := filepath.Join(rootDir, "data")
	passwordFile := filepath.Join(rootDir, "admin-password")
	if err := os.WriteFile(passwordFile, []byte(c.adminPassword+"\n"), 0600); err != nil {
		return fmt.Errorf("writing local PostgreSQL password file: %w", err)
	}
	initCmd := exec.CommandContext(ctx, c.initdbTool, "-D", dataDir, "-U", "postgres",
		"--auth-local=scram-sha-256", "--auth-host=scram-sha-256",
		"--pwfile", passwordFile, "--no-sync")
	if output, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initializing local PostgreSQL: %w: %s", err, output)
	}
	if err := os.Remove(passwordFile); err != nil {
		return fmt.Errorf("removing local PostgreSQL password file: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("selecting local PostgreSQL port: %w", err)
	}
	c.host, c.port, err = net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	if err != nil {
		return fmt.Errorf("parsing local PostgreSQL port: %w", err)
	}
	c.runCmd = exec.Command(c.tool, "-D", dataDir, "-h", c.host, "-p", c.port,
		"-c", "unix_socket_directories="+dataDir, "-c", "fsync=off")
	c.runCmd.Stdout = os.Stdout
	c.runCmd.Stderr = os.Stderr
	if err := c.runCmd.Start(); err != nil {
		return fmt.Errorf("starting local PostgreSQL: %w", err)
	}
	return nil
}

func (c *Container) Stop(ctx context.Context) error {
	defer func() {
		if c.configFile != "" {
			_ = os.Remove(c.configFile)
		}
	}()

	for _, inst := range c.instances {
		if err := inst.Close(ctx); err != nil {
			c.logger.Error(err, "closing database instance")
		}
	}

	if c.adminConn != nil {
		_, _ = c.adminConn.Exec(ctx, "ALTER DATABASE metering_template IS_TEMPLATE false")
		_, _ = c.adminConn.Exec(ctx, "DROP DATABASE IF EXISTS metering_template WITH (FORCE)")
		_, _ = c.adminConn.Exec(ctx, fmt.Sprintf("DROP USER IF EXISTS %s", containerUser))
		_ = c.adminConn.Close(ctx)
	}
	if c.local {
		return c.stopLocal(ctx)
	}

	killCmd := exec.Command(c.tool, "kill", c.id)
	if err := killCmd.Run(); err != nil {
		c.logger.Error(err, "killing database container")
	}
	if c.runCmd != nil {
		_ = c.runCmd.Wait()
	}

	return nil
}

func (c *Container) stopLocal(ctx context.Context) error {
	var stopErr error
	if c.runCmd != nil && c.runCmd.Process != nil {
		if err := c.runCmd.Process.Signal(os.Interrupt); err != nil {
			stopErr = fmt.Errorf("signaling local PostgreSQL: %w", err)
			_ = c.runCmd.Process.Kill()
		}
		done := make(chan error, 1)
		go func() { done <- c.runCmd.Wait() }()
		select {
		case <-done:
		case <-ctx.Done():
			_ = c.runCmd.Process.Kill()
			<-done
			stopErr = errors.Join(stopErr, ctx.Err())
		}
	}
	if c.dataDir != "" {
		stopErr = errors.Join(stopErr, os.RemoveAll(c.dataDir))
	}
	return stopErr
}

type Instance struct {
	container *Container
	name      string
	url       string
	lock      sync.Mutex
}

func (c *Container) NewInstance() *Instance {
	inst := &Instance{container: c}
	c.lock.Lock()
	c.instances = append(c.instances, inst)
	c.lock.Unlock()
	return inst
}

func (i *Instance) init(ctx context.Context) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	if i.name != "" {
		return nil
	}

	i.container.lock.Lock()
	i.container.count++
	i.name = fmt.Sprintf("metering_test%d", i.container.count)
	i.container.lock.Unlock()

	i.url = fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		containerUser, i.container.userPassword, i.container.host, i.container.port, i.name,
	)

	_, err := i.container.adminConn.Exec(ctx,
		fmt.Sprintf("CREATE DATABASE %s TEMPLATE metering_template OWNER %s",
			i.name, containerUser))
	if err != nil {
		return fmt.Errorf("creating database %s from template: %w", i.name, err)
	}
	return nil
}

func (i *Instance) Pool(ctx context.Context) (*pgxpool.Pool, error) {
	if err := i.init(ctx); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, i.url)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	return pool, nil
}

func (i *Instance) Close(ctx context.Context) error {
	i.lock.Lock()
	defer i.lock.Unlock()
	if i.name == "" {
		return nil
	}
	_, err := i.container.adminConn.Exec(ctx,
		fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", i.name))
	if err != nil {
		return fmt.Errorf("dropping database %s: %w", i.name, err)
	}
	return nil
}

func (c *Container) waitForPort(ctx context.Context) error {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out waiting for container port")
		case <-ticker.C:
			portOut := &bytes.Buffer{}
			portCmd := exec.CommandContext(ctx, c.tool, "port", c.id, "5432/tcp")
			portCmd.Stdout = portOut
			if err := portCmd.Run(); err != nil {
				continue
			}
			lines := strings.Split(portOut.String(), "\n")
			if len(lines) < 1 || strings.TrimSpace(lines[0]) == "" {
				continue
			}
			host, port, err := net.SplitHostPort(strings.TrimSpace(lines[0]))
			if err != nil {
				continue
			}
			if host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			c.host = host
			c.port = port
			return nil
		}
	}
}

func (c *Container) waitForConnection(ctx context.Context) error {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	adminURL := fmt.Sprintf(
		"postgres://postgres:%s@%s:%s/postgres?sslmode=disable&connect_timeout=1",
		c.adminPassword, c.host, c.port,
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out waiting for database connection")
		case <-ticker.C:
			conn, err := pgx.Connect(ctx, adminURL)
			if err != nil {
				continue
			}
			c.adminConn = conn
			return nil
		}
	}
}

func selectTool() (string, error) {
	for _, tool := range []string{"podman", "docker"} {
		path, err := exec.LookPath(tool)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("neither podman nor docker found in PATH")
}

func createConfigFile() (string, error) {
	f, err := os.CreateTemp("", "osac-metering-db-*.conf")
	if err != nil {
		return "", fmt.Errorf("creating config file: %w", err)
	}
	if _, err := f.WriteString(containerConfigText); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("writing config file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("closing config file: %w", err)
	}
	if err := os.Chmod(f.Name(), 0644); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("setting config file permissions: %w", err)
	}
	return f.Name(), nil
}

func randomHex() string {
	return fmt.Sprintf("%016x%016x", rand.Uint64(), rand.Uint64())
}

const containerUser = "metering"

const containerImage = "quay.io/sclorg/postgresql-18-c10s" +
	"@sha256:6be2c9d855f06fb665257a6b0911676a38d740be7022cc61acee1c99a832b1b2"

const containerConfigPath = "/opt/app-root/src/postgresql-cfg/custom.conf"

const containerConfigText = `
fsync = off
log_destination = 'stderr'
log_statement = 'all'
logging_collector = off
`
