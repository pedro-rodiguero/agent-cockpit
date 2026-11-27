#!/usr/bin/env bash
# Sets up your own to-do server on this machine: asks for its domain, makes the key that protects
# two-step verification, creates the account and starts the server behind Caddy.
#
#   ./setup.sh [todo.example.com]
#
# Needs Docker with the compose plugin, ports 80 and 443 open, and a DNS record pointing the domain at
# this machine. Run it again any time: it keeps the settings and the data it finds.
set -euo pipefail
cd "$(dirname "$0")"

command -v docker >/dev/null || { echo "setup: Docker is not installed (https://docs.docker.com/engine/install/)" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "setup: the Docker compose plugin is missing" >&2; exit 1; }

if [[ ! -f .env ]]; then
	domain="${1:-}"
	[[ -n "$domain" ]] || read -rp "Domain of your to-do server (for example todo.example.com): " domain
	domain="${domain#https://}"
	domain="${domain%/}"
	[[ "$domain" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || { echo "setup: '$domain' is not a domain name" >&2; exit 1; }
	# The key encrypts the two-step verification secret: lose it and 2FA has to be set up again.
	(umask 077 && printf 'TODO_DOMAIN=%s\nTODO_ENC_KEY=%s\n' "$domain" "$(openssl rand -hex 32)" >.env)
	echo "✓ wrote .env (keep a copy of it with your backups)"
fi
# shellcheck disable=SC1091
source .env

docker compose build
# .account marks that the account was created, so running this again never asks to replace the password.
if [[ ! -f .account ]]; then
	docker compose up -d caddy
	docker compose stop todo >/dev/null 2>&1 || true
	echo "Choose the password of your to-do list (12 characters or more, not a common one):"
	docker compose run --rm todo set-password
	touch .account
fi
docker compose up -d

cat <<DONE

✓ your to-do server is at https://$TODO_DOMAIN

  On the phone    open that address in Safari, sign in, then Share › Add to Home Screen
  On the computer cockpit todo connect https://$TODO_DOMAIN
                  (it asks for a device token: make one in the app, Settings › Devices)

  Change the password  docker compose stop todo && docker compose run --rm todo set-password && docker compose start todo
  Update               git pull && ./setup.sh
  Back up              the todo-data volume and .env
DONE
