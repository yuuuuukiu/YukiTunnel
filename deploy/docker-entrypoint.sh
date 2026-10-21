#!/bin/sh
# Idempotent bootstrap for the official image:
#   * first run: create config, TLS material and the admin account
#   * every run: exec the server in the foreground
set -eu

# The database holds password hashes and API tokens; keep new files private
# even though /data is only reachable by the container user.
umask 077

DATA_DIR="${YUKI_DATA_DIR:-/data}"

# Read-only subcommands must not bootstrap: "docker run <image> --version" or
# "doctor" is a diagnostic call, and creating config/TLS/database as a side
# effect is both surprising and slow.
case "${1:-}" in
  --version|-v|version|--help|-h|help|completion|doctor)
    exec yuki-server "$@"
    ;;
esac

mkdir -p "$DATA_DIR"

# `bootstrap` has its own positional parameters so the caller's "$@" (the
# container command) is preserved for the final exec.
bootstrap() {
  echo "[entrypoint] initialising YukiTunnel in $DATA_DIR"
  set -- init \
    --data-dir "$DATA_DIR" \
    --api-addr "${YUKI_API_ADDR:-0.0.0.0:8443}" \
    --control-addr "${YUKI_CONTROL_ADDR:-0.0.0.0:9443}" \
    --public-host "${YUKI_PUBLIC_HOST:-}" \
    --admin-user "${YUKI_ADMIN_USERNAME:-admin}" \
    --port-min "${YUKI_PORT_MIN:-20000}" \
    --port-max "${YUKI_PORT_MAX:-30000}"
  if [ -n "${YUKI_ADMIN_PASSWORD:-}" ]; then
    set -- "$@" --admin-password "$YUKI_ADMIN_PASSWORD"
  fi
  if [ -n "${YUKI_DOMAIN_SUFFIX:-}" ]; then
    set -- "$@" --domain-suffix "$YUKI_DOMAIN_SUFFIX"
  fi
  if [ "${YUKI_DOMAIN_BINDING:-false}" = "true" ]; then
    set -- "$@" --domain-binding
  fi
  yuki-server "$@"
}

if [ ! -f "$DATA_DIR/config.yaml" ]; then
  bootstrap
fi

# Re-apply the domain switch on every start so `.env` stays authoritative.
if [ "${YUKI_DOMAIN_BINDING:-}" = "true" ]; then
  yuki-server domain binding on --data-dir "$DATA_DIR" --suffix "${YUKI_DOMAIN_SUFFIX:-}" >/dev/null 2>&1 || true
elif [ "${YUKI_DOMAIN_BINDING:-}" = "false" ]; then
  yuki-server domain binding off --data-dir "$DATA_DIR" >/dev/null 2>&1 || true
fi

exec yuki-server "$@"
