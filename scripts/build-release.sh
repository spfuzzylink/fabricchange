#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:-}"
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]]; then
  echo "usage: scripts/build-release.sh v0.1.0-alpha.1" >&2
  exit 2
fi
name="fabricchange"
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' EXIT
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -buildvcs=false -trimpath -ldflags="-s -w -buildid= -X main.buildVersion=$version" -o "$stage/$name" "./cmd/$name"
  cp LICENSE NOTICE README.md "$stage/"
  cp -R examples "$stage/"
  if [[ -d docs ]]; then cp -R docs "$stage/"; fi
  tar -czf "dist/${name}_${version}_${os}_${arch}.tar.gz" -C "$stage" .
  rm -rf "$stage"
  trap - EXIT
done
python3 - "$name" "$version" <<'PYTHON'
import hashlib,pathlib,sys
root=pathlib.Path('dist')
files=sorted(root.glob(sys.argv[1]+'_'+sys.argv[2]+'_*.tar.gz'))
(root/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in files))
PYTHON
