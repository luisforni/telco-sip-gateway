# Telco SIP Gateway

SIP/RTP gateway based on FreeSWITCH + Kamailio for high-volume call ingestion with transcoding, load distribution and SRTP encryption.

## Architecture

```
PSTN / SIP Trunk
       │
   Kamailio (SBC/Proxy)  ←── Registration, rate-limit, auth
       │
  FreeSWITCH (Media)     ←── RTP, transcoding, jitter-buffer
       │
  Kafka (call-events)    ←── Async fan-out to downstream services
```

## Key Features
- SIP RFC 3261 / 3264 / 3581
- SRTP + TLS (SIPS) encryption
- G.711 / G.722 / Opus / G.729 transcoding
- WebRTC gateway (ICE, DTLS-SRTP)
- SIP OPTIONS heartbeat
- Active call count metrics (Prometheus `/metrics`)

## Quick Start

```bash
docker compose up -d
```

### Environment Variables
| Variable | Default | Description |
|---|---|---|
| `SIP_DOMAIN` | `sip.telco.local` | SIP domain |
| `KAFKA_BROKERS` | `kafka:9092` | Kafka bootstrap servers |
| `EXTERNAL_IP` | auto-detect | Public IP for SDP |
| `MAX_SESSIONS` | `10000` | Max concurrent calls |
| `TLS_CERT_FILE` | `/etc/ssl/sip/cert.pem` | TLS certificate |
| `TLS_KEY_FILE` | `/etc/ssl/sip/key.pem` | TLS private key |

## Ports
| Port | Protocol | Purpose |
|---|---|---|
| 5060 | UDP/TCP | SIP signaling |
| 5061 | TCP | SIPS (TLS) |
| 16384-32768 | UDP | RTP media |
| 8080 | HTTP | Health + metrics |
