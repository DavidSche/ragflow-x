#!/bin/sh
# Container entrypoint.
#
# The image's configuration contract is "mount a file at /app/config/config.yaml".
# Without one the server used to exit with
#   load config failed: open /app/config/config.yaml: no such file or directory
# which reads like a broken image instead of "nothing is configured yet". Say
# which of the two it is, then start: built-in defaults put the server into the
# first-run setup wizard (same behavior as an unpacked archive with no config).
set -e

CONFIG=/app/config/config.yaml

# Operator commands and explicit flags belong to the binary untouched:
# `docker run --rm <image> reset-admin-password -list` is the password recovery
# path, and it must not be rewritten into "-config ... reset-admin-password".
case "${1:-}" in
  -* | reset-admin-password | version | help) exec ragflow-x "$@" ;;
esac

if [ -f "$CONFIG" ]; then
  exec ragflow-x -config "$CONFIG" "$@"
fi

echo "no config mounted at $CONFIG: starting on the built-in defaults" >&2
echo "  - the web UI offers the first-run setup wizard on http://localhost:${RGX_PORT:-9191}/" >&2
echo "  - to configure by hand, mount a file at $CONFIG (config.example.yaml is in the image)" >&2
exec ragflow-x "$@"
