package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

const remoteKubeconfigWatchInterval = 5 * time.Second

func readRemoteKubeconfigDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read remote cluster kubeconfig %q: %w", path, err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// watchRemoteKubeconfig returns true after the mounted kubeconfig changes.
// The manager's remote cluster is constructed at startup, so its process must
// restart to use newly projected Secret contents.
func watchRemoteKubeconfig(ctx context.Context, path, initialDigest string, interval time.Duration) bool {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		digest, err := readRemoteKubeconfigDigest(path)
		if err != nil {
			setupLog.Error(err, "unable to read remote cluster kubeconfig while watching for updates")
		} else if digest != initialDigest {
			setupLog.Info("remote cluster kubeconfig changed; restarting operator")
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
