package main

import (
	"fmt"
	"log/slog"
	"os"
)

var AppVersion string = "4.0.1"

// checkAndFixFilePermissions ensures sensitive files have restricted permissions. (SA-08)
func checkAndFixFilePermissions(paths []string) {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue // file doesn't exist yet, will be created with correct perms
		}
		mode := info.Mode().Perm()
		if mode != 0600 {
			slog.Warn("fixing file permissions", "path", path, "from", fmt.Sprintf("%04o", mode), "to", "0600")
			if err := os.Chmod(path, 0600); err != nil {
				slog.Error("failed to fix file permissions", "path", path, "error", err)
			}
		}
	}
}

func main() {
	initCore()
	initAllFederation()
	initAllNetwork()
	startBackgroundTasks()
	runServer()
}
