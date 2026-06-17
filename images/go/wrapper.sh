#!/bin/sh
set -e
cp -r /input/. /sandbox/
mkdir -p "$GOTMPDIR"
if [ -n "$BUILD_CMD" ]; then
	sh -c "$BUILD_CMD" || exit 100 # signal build failure with exit code 100
fi
exec "$@"
