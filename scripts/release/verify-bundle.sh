#!/bin/sh
set -eu

releasebundle=${RELEASEBUNDLE_BIN:-releasebundle}
exec "$releasebundle" verify "$@"
