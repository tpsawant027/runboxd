#!/bin/sh
set -e
cp -r /input/. /sandbox/
if [ -n "$BUILD_CMD" ]; then
	sh -c "$BUILD_CMD"
fi
exec "$@"
