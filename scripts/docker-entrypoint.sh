#!/bin/sh
set -eu

secret_file="${AIOPS_SECRET_KEY_FILE:-/data/secret-key}"
secret_key="${AIOPS_SECRET_KEY:-}"

if [ -z "$secret_key" ]; then
  if [ ! -s "$secret_file" ]; then
    secret_dir="$(dirname "$secret_file")"
    temporary_file="${secret_file}.tmp.$$"
    mkdir -p "$secret_dir"
    umask 077
    od -An -N16 -tx1 /dev/urandom | tr -d '[:space:]' > "$temporary_file"
    chmod 600 "$temporary_file"
    mv "$temporary_file" "$secret_file"
    echo "已生成持久化加密密钥：$secret_file"
  fi

  chmod 600 "$secret_file"
  secret_key="$(cat "$secret_file")"
fi

secret_bytes="$(printf '%s' "$secret_key" | wc -c | tr -d '[:space:]')"
if [ "$secret_bytes" -ne 32 ]; then
  echo "AIOPS_SECRET_KEY 必须正好为 32 个字节，当前为 ${secret_bytes} 个字节。" >&2
  exit 1
fi

export AIOPS_SECRET_KEY="$secret_key"
exec "$@"
