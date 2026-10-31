#!/bin/sh
set -e

if ! getent passwd idrometer >/dev/null; then
    adduser --system --group --quiet --no-create-home \
        --home /var/lib/idrometer --shell /usr/sbin/nologin idrometer
fi

install -d -m 0750 -o root -g idrometer /etc/idrometer
# systemd's StateDirectory= would create this too, but `idrometer-ctl` may run
# before the service ever has (or on a system without systemd).
install -d -m 0750 -o idrometer -g idrometer /var/lib/idrometer

# Create the config once, with a fresh random ingest token. Never overwrite it
# on upgrade: it is the user's file.
if [ ! -f /etc/idrometer/idrometer.env ]; then
    token=$(head -c 32 /dev/urandom | base64 | tr -d '/+=\n' | head -c 32)
    umask 027
    sed "s/@TOKEN@/${token}/" /usr/share/idrometer/idrometer.env.example \
        >/etc/idrometer/idrometer.env
    chown root:idrometer /etc/idrometer/idrometer.env
    chmod 0640 /etc/idrometer/idrometer.env
fi

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable idrometer.service >/dev/null 2>&1 || true
    # Restart on upgrade (a previous version is passed as $2), start on first install.
    systemctl restart idrometer.service || true
fi
