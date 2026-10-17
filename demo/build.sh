#!/bin/sh
# Собирает _site/ — тренировочное демо для GitHub Pages.
# index.html = app.html + mock.js первым скриптом; пути скриптов относительные;
# регистрация service worker вырезана (на Pages она смотрит на корень origin).
set -e
cd "$(dirname "$0")/.."
rm -rf _site
mkdir -p _site
cp demo/mock.js internal/server/web/jsQR.js _site/
cp internal/server/web/zxing.min.js _site/zxing.js
python3 - <<'EOF'
import pathlib
import re

h = pathlib.Path('internal/server/web/app.html').read_text()
h = h.replace('<head>', '<head><script src="mock.js"></script>', 1)
h = h.replace('src="/jsQR.js"', 'src="jsQR.js"').replace('src="/zxing.js"', 'src="zxing.js"')
h, n = re.subn(
    r"if\('serviceWorker' in navigator.*?register\('/sw\.js'\)\.catch\(\(\)=>\{\}\);\n",
    '', h, flags=re.S)
assert n == 1, 'service worker registration not found'
assert 'src="mock.js"' in h and '/jsQR.js' not in h
pathlib.Path('_site/index.html').write_text(h)
EOF
echo "собрано: $(ls _site | tr '\n' ' ')"
