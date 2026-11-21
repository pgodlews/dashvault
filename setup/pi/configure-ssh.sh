#!/bin/bash -eu
set -o pipefail

setup_progress "configuring ssh"

# If requested, add the desired SSH public key into /root/.ssh/authorized_keys
if [ -n "${SSH_ROOT_PUBLIC_KEY:-}" ]
then
  ssh_dir='/root/.ssh'
  mkdir -p $ssh_dir
  echo "${SSH_ROOT_PUBLIC_KEY}" > "${ssh_dir}/authorized_keys"
fi

DEFUSER=$(grep ":1000:1000:" /etc/passwd | awk -F : '{print $1}')

if [ -n "${PI_PASSWORD:-}" ] && [ -n "$DEFUSER" ]; then
    setup_progress "Setting custom password for $DEFUSER"
    echo "$DEFUSER:$PI_PASSWORD" | chpasswd
elif [ -t 0 ] && [ -n "$DEFUSER" ]; then
    echo "============================================================"
    echo "OS User Authentication"
    echo "The default Raspberry Pi login is highly insecure."
    echo "Do you want to change the password for '$DEFUSER' now? (Y/n)"
    echo "============================================================"
    read -r -p "Change password? [Y/n]: " change_pass
    if [[ "$change_pass" =~ ^[Yy]$ ]] || [[ -z "$change_pass" ]]; then
        passwd "$DEFUSER"
    fi
fi

if [ -t 0 ]; then
    echo "============================================================"
    echo "SSH Security"
    echo "Do you want to disable SSH password authentication entirely?"
    echo "(Only do this if you have set up SSH keys!)"
    echo "============================================================"
    read -r -p "Disable SSH password auth? [y/N]: " disable_pass_auth
    if [[ "$disable_pass_auth" =~ ^[Yy]$ ]]; then
        SSH_DISABLE_PASSWORD_AUTHENTICATION="true"
    fi
fi

# If requested, disable SSH Password Authentication
if [ "${SSH_DISABLE_PASSWORD_AUTHENTICATION:-false}" = "true" ]
then
  setup_progress "Disabling SSH password authentication"
  # Support older sshd_config where it is commented out, and newer ones where it might be active
  sed -i -E 's/^#?PasswordAuthentication yes/PasswordAuthentication no/' '/etc/ssh/sshd_config'
  systemctl reload sshd || true
fi

setup_progress "done configuring ssh"
