#!/bin/sh
set -e
cp -r /input/. /sandbox/
exec "$@"
