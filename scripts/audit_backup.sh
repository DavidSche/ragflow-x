#!/usr/bin/env bash
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${RGX_AUDIT_BACKUP_DIR:?RGX_AUDIT_BACKUP_DIR is required}"

backup_dir=${RGX_AUDIT_BACKUP_DIR}
mkdir -p "${backup_dir}"

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_file="${backup_dir}/rgx-audit-log-${timestamp}.dump"
manifest_file="${backup_file}.sha256"

pg_dump --format=custom --serializable-deferrable --table=rgx_audit_log --file="${backup_file}" "${DATABASE_URL}"
if [[ ! -s "${backup_file}" ]]; then
    echo "pg_dump created an empty backup" >&2
    exit 1
fi

file_hash=$(sha256sum "${backup_file}" | awk '{print $1}')
file_size=$(wc -c < "${backup_file}" | tr -d ' ')
printf 'sha256:%s\nsize:%s\ntimestamp:%s\n' "${file_hash}" "${file_size}" "${timestamp}" > "${manifest_file}"

echo "backup=${backup_file}"
echo "manifest=${manifest_file}"
echo "sha256=${file_hash}"
