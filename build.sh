#!/bin/sh
# Сборка xray-selector: UI → три архитектуры → архив с исходниками.
set -e
cd "$(dirname "$0")"
(cd ui && cat a_head.html b_body.html c_core.js d1_pages.js d2_pages.js d3_pages.js d3b_rules.js d3c_wizard.js d4_pages.js > ../ui.html)
if command -v node >/dev/null && command -v python3 >/dev/null; then
  python3 -c "import re;s=open('ui.html').read();open('/tmp/xsel-ui-check.js','w').write('\n;\n'.join(re.findall(r'<script>(.*?)</script>',s,re.S)))"
  node --check /tmp/xsel-ui-check.js || { echo 'ОШИБКА в JavaScript интерфейса'; exit 1; }
fi
go vet . && go test .
for a in arm64 mipsle mips; do
  if [ $a = arm64 ]; then CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/xray-selector/xray-selector-arm64 .
  else CGO_ENABLED=0 GOOS=linux GOARCH=$a GOMIPS=softfloat go build -trimpath -ldflags "-s -w" -o dist/xray-selector/xray-selector-$a .; fi
done
cp packaging/install.sh packaging/S99xray-selector dist/xray-selector/ && chmod 755 dist/xray-selector/install.sh dist/xray-selector/S99xray-selector
rm -rf dist/xray-selector/src && mkdir -p dist/xray-selector/src
tar cf - --exclude=./dist --exclude='./*.tar.gz' . | tar xf - -C dist/xray-selector/src
tar czf xray-selector.tar.gz -C dist xray-selector
ls -la xray-selector.tar.gz
