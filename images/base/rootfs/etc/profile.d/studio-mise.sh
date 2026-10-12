# Template toolchains live in /opt/mise, not in the agent's home (see the Dockerfile). Login
# shells of the agent get mise's shims and, in bash, its per-directory activation.
if [ "$(id -u)" -ne 0 ]; then
    export MISE_DATA_DIR=/opt/mise
    export MISE_CONFIG_DIR=/opt/mise/config
    export MISE_INSTALLS_DIR=/opt/mise/installs
    export MISE_SHIMS_DIR=/opt/mise/shims
    export PATH=/opt/mise/shims:$PATH
    if [ -n "${BASH_VERSION:-}" ]; then
        eval "$(/usr/local/bin/mise activate bash)"
    fi
fi
