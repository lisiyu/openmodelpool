#!/bin/bash
# Self-restart script: kills current process and starts new one
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PID_FILE="$SCRIPT_DIR/data/server.pid"

# Start new process
cd "$SCRIPT_DIR"
nohup ./openmodelpool >> server.log 2>&1 &
NEW_PID=$!
echo "Started new process with PID $NEW_PID"

# Kill old process (the one that called this script)
if [ -n "$1" ]; then
    sleep 0.5
    kill "$1" 2>/dev/null
    echo "Killed old process $1"
fi
