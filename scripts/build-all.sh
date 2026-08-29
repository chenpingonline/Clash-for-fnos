#!/bin/bash
set -euo pipefail
exec "$(cd "$(dirname "$0")" && pwd)/build-manual.sh" all
