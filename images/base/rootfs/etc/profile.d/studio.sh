# Trust the system store (which includes the Studio CA) everywhere.
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
export NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt
export REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
export GIT_SSL_CAINFO=/etc/ssl/certs/ca-certificates.crt
# Long enough to survive a held connection while the user approves it.
export PIP_DEFAULT_TIMEOUT=60
