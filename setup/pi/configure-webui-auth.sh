#!/bin/bash -eu
set -o pipefail

setup_progress "configuring web UI basic authentication"

AUTH_DIR="/etc/nginx/auth"
AUTH_FILE="$AUTH_DIR/.htpasswd"
NGINX_CONF="/etc/nginx/sites-available/teslausb.nginx"

# Backward compatibility with upstream variables
USERNAME="${WEBUI_USERNAME:-${WEB_USERNAME:-}}"
PASSWORD="${WEBUI_PASSWORD:-${WEB_PASSWORD:-}}"

# If no credentials provided via env, and we are interactive, prompt for them
if [ -z "$USERNAME" ] && [ -t 0 ]; then
    echo "============================================================"
    echo "Web UI Authentication"
    echo "Credentials are required to protect the web interface."
    echo "Leave username blank to keep existing credentials."
    echo "============================================================"
    read -r -p "Enter a username for the Web UI: " prompt_user
    if [ -n "$prompt_user" ]; then
        USERNAME="$prompt_user"
        while true; do
            read -r -s -p "Enter password for $USERNAME: " prompt_pass1
            echo
            read -r -s -p "Confirm password: " prompt_pass2
            echo
            if [ "$prompt_pass1" = "$prompt_pass2" ] && [ -n "$prompt_pass1" ]; then
                PASSWORD="$prompt_pass1"
                break
            else
                echo "Passwords do not match or are empty. Try again."
            fi
        done
    fi
fi

mkdir -p "$AUTH_DIR"

if [ -n "$USERNAME" ] && [ -n "$PASSWORD" ]; then
    setup_progress "Enabling basic authentication for Web UI."
    printf '%s\n' "$PASSWORD" | htpasswd -ic "$AUTH_FILE" "$USERNAME"

    # Secure the htpasswd file
    chown www-data:www-data "$AUTH_FILE"
    chmod 600 "$AUTH_FILE"

elif [ -s "$AUTH_FILE" ]; then
    setup_progress "Keeping existing Web UI credentials."
else
    setup_progress "Web UI credentials required: set WEBUI_USERNAME and WEBUI_PASSWORD."
    exit 1
fi

# Re-enable authentication on devices previously configured without it.
sed -i 's/auth_basic off;/auth_basic "teslausb";/' "$NGINX_CONF"
