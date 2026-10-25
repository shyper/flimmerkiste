#!/bin/sh
# Frontend tests (pages in both languages, editor, catalogs) and the build.
set -eu
cd web
npm ci --no-audit --no-fund
npm test
npm run build
