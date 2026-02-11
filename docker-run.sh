#!/bin/bash
set -e

if [[ -z "$GID" ]]; then
    GID="$UID"
fi

function fixperms {
    chown -R $UID:$GID /data

    # Disable file logging if pointing to read-only location
    if [[ "$(yq e '.logging.writers[1].filename' /data/config.yaml 2>/dev/null)" == "./logs/mautrix-chatgpt.log" ]]; then
        yq -I4 e -i 'del(.logging.writers[1])' /data/config.yaml
    fi
}

if [[ ! -f /data/config.yaml ]]; then
    /usr/bin/mautrix-chatgpt -c /data/config.yaml -e
    echo "Didn't find a config file."
    echo "Copied default config file to /data/config.yaml"
    echo "Modify that config file to your liking."
    echo "Start the container again after that to generate the registration file."
    exit
fi

if [[ ! -f /data/registration.yaml ]]; then
    /usr/bin/mautrix-chatgpt -g -c /data/config.yaml -r /data/registration.yaml || exit $?
    echo "Didn't find a registration file."
    echo "Generated one for you."
    echo "See https://docs.mau.fi/bridges/general/registering-appservices.html on how to use it."
    exit
fi

cd /data
fixperms

echo "Starting mautrix-chatgpt (API mode)"
exec gosu $UID:$GID /usr/bin/mautrix-chatgpt -c /data/config.yaml
