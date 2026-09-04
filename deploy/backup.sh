#!/usr/bin/env bash
# Backup PostgreSQL hang dem. Tu host Postgres nghia la ban tu chiu trach nhiem backup.
#   crontab -e  =>  0 3 * * * /home/ubuntu/PaySplit-BE/deploy/backup.sh
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${BACKUP_DIR:-$HOME/paysplit-backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"
mkdir -p "$OUT"

STAMP=$(date +%Y%m%d-%H%M%S)
docker compose -f "$DIR/docker-compose.prod.yaml" exec -T postgres \
	pg_dump -U paysplit -d paysplit --no-owner --no-acl -Fc \
	> "$OUT/paysplit-$STAMP.dump"

find "$OUT" -name 'paysplit-*.dump' -mtime "+$KEEP_DAYS" -delete
echo "backup ok: $OUT/paysplit-$STAMP.dump"
