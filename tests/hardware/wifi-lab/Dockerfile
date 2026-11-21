FROM debian:trixie-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends hostapd dnsmasq-base iw iproute2 iptables python3 \
    && rm -rf /var/lib/apt/lists/*
COPY lab.py /lab.py
ENTRYPOINT ["python3", "-u", "/lab.py"]
