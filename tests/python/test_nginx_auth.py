"""Guards on how the web server authenticates requests.

The behaviour itself (lockout, 401 challenge, fail closed) was checked against a
real nginx; these keep the shipped config and the setup fallback in step.
"""

from __future__ import annotations

import re
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
NGINX = (REPO / "teslausb-www" / "teslausb.nginx").read_text()
CONFIGURE_WEB = (REPO / "setup" / "pi" / "configure-web.sh").read_text()


def active(directive: str, text: str) -> list[str]:
    return re.findall(rf"^\s*{directive}\b[^\n]*;", text, flags=re.MULTILINE)


def test_every_request_goes_through_the_backend_gate() -> None:
    server_level = NGINX[: NGINX.index("location")]
    assert active("auth_request", server_level) == ["    auth_request /_auth_gate;"]
    # auth_basic runs before auth_request, so having both would hide wrong
    # passwords from the backend's lockout counter.
    assert active("auth_basic", NGINX) == []
    assert "auth_basic off" not in NGINX
    gate = NGINX[NGINX.index("location = /_auth_gate") :]
    assert "internal;" in gate
    assert "proxy_pass http://127.0.0.1:8080/api/auth/check;" in gate
    assert "proxy_set_header X-Real-IP $remote_addr;" in gate
    assert "proxy_pass_request_body off;" in gate


def test_setup_fallback_leaves_password_checking_on() -> None:
    assert "--with-http_auth_request_module" in CONFIGURE_WEB
    assert "-e '/# BEGIN auth gate/,/# END auth gate/d'" in CONFIGURE_WEB
    assert r"-e 's/^\( *\)#fallback# /\1/'" in CONFIGURE_WEB

    # The same edit setup makes with sed when nginx has no auth_request module.
    fallback = re.sub(r"(?ms)^[^\n]*# BEGIN auth gate.*?# END auth gate\n", "", NGINX)
    fallback = re.sub(r"(?m)^( *)#fallback# ", r"\1", fallback)
    assert active("auth_request", fallback) == []
    assert "/_auth_gate" not in fallback
    assert active("auth_basic", fallback) == ['    auth_basic "teslausb";']
    assert active("auth_basic_user_file", fallback) == [
        "    auth_basic_user_file /etc/nginx/auth/.htpasswd;"
    ]
    assert fallback.count("{") == fallback.count("}")
