# Third-party runtime notices

The self-contained macOS package includes these pinned runtime components:

| Component | Version | License | Source |
|---|---:|---|---|
| CPython standalone build | 3.13.14, build 20260623 | Python Software Foundation License | https://github.com/astral-sh/python-build-standalone |
| Node.js | 22.23.1 | MIT | https://nodejs.org/download/release/v22.23.1/ |
| curl_cffi | 0.15.0 | MIT | https://pypi.org/project/curl-cffi/0.15.0/ |
| certifi | 2026.6.17 | MPL-2.0 | https://pypi.org/project/certifi/2026.6.17/ |
| cffi | 2.1.0 | MIT | https://pypi.org/project/cffi/2.1.0/ |
| markdown-it-py | 4.2.0 | MIT | https://pypi.org/project/markdown-it-py/4.2.0/ |
| mdurl | 0.1.2 | MIT | https://pypi.org/project/mdurl/0.1.2/ |
| pycparser | 3.0 | BSD-3-Clause | https://pypi.org/project/pycparser/3.0/ |
| Pygments | 2.20.0 | BSD-2-Clause | https://pypi.org/project/Pygments/2.20.0/ |
| Rich | 15.0.0 | MIT | https://pypi.org/project/rich/15.0.0/ |
| sweet-cookie | 0.4.0 | MIT | https://www.npmjs.com/package/@steipete/sweet-cookie/v/0.4.0 |

The package retains the license files distributed with CPython, Node.js, the Python wheels, and the npm package. Transitive Python packages are pinned with hashes in `packaging/runtime-requirements.txt`; the Node dependency is pinned by `sidecar/package-lock.json`.
