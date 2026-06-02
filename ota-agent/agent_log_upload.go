package main

import (
	"path/filepath"
	"strings"
)

func normalizeAgentLogUploadPaths(c *AgentConfig) {
	if c == nil {
		return
	}
	root, err := agentInstallRoot()
	if err != nil {
		return
	}

	resolveRel := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return p
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		if strings.HasPrefix(p, "../") || strings.HasPrefix(p, "./") {
			if exeDir, err := getExecutableDir(); err == nil {
				return filepath.Clean(filepath.Join(exeDir, p))
			}
		}
		return filepath.Clean(filepath.Join(root, p))
	}

	defaultScanDir := filepath.Join(root, "logs")
	c.LogUpload.ScanDir = resolveRel(c.LogUpload.ScanDir)
	if strings.TrimSpace(c.LogUpload.ScanDir) == "" {
		c.LogUpload.ScanDir = defaultScanDir
	}
}
