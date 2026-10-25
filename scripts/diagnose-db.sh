#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$project_dir"
container_id="$(docker compose ps -q postgres)"
if [[ -z "$container_id" ]]; then
  echo 'PostgreSQL container is not running.' >&2
  exit 1
fi
published="$(docker compose port postgres 5432)"
container_ips="$(docker inspect --format '{{range .NetworkSettings.Networks}}{{println .IPAddress}}{{end}}' "$container_id")"
read -r container_ip <<< "$container_ips"
echo '=== PostgreSQL inside the container ==='
docker compose exec -T postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" psql -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT 1"'
python3 - "$published" "$container_ip" <<'PY'
import os, socket, struct, sys

# This probe never sends a password or reads .env. The dummy user/database
# intentionally test protocol responses, independently of credentials.
published, container_ip = sys.argv[1:]
host, port = published.rsplit(':', 1)
targets = [(host.strip('[]'), int(port)), (container_ip, 5432)]

def read_exact(sock, count):
    result = b''
    while len(result) < count:
        part = sock.recv(count-len(result))
        if not part:
            raise EOFError('peer closed the connection')
        result += part
    return result

for host, port in targets:
    print(f'=== Host -> {host}:{port} ===', flush=True)
    for label, version in [('SSLRequest', None), ('Startup 3.0', 196608), ('Startup 3.2', 196610)]:
        try:
            with socket.create_connection((host, port), timeout=3) as sock:
                sock.settimeout(3)
                if version is None:
                    sock.sendall(struct.pack('!II', 8, 80877103))
                    response = read_exact(sock, 1)
                    print(f'{label}: {response!r} (PostgreSQL expects S or N)', flush=True)
                else:
                    payload = struct.pack('!I', version) + b'user\0ai_trader_probe\0database\0ai_trader_probe\0\0'
                    sock.sendall(struct.pack('!I', len(payload)+4) + payload)
                    header = read_exact(sock, 5)
                    kind, length = chr(header[0]), struct.unpack('!I', header[1:])[0]
                    if not 4 <= length <= 65536:
                        print(f'{label}: non-PostgreSQL header {header.hex()}', flush=True)
                        continue
                    body = read_exact(sock, length-4)
                    if kind == 'R' and len(body) >= 4:
                        print(f'{label}: authentication request, method={struct.unpack("!I", body[:4])[0]}', flush=True)
                    elif kind == 'E':
                        # Only print SQLSTATE, never server message or credentials.
                        code = next((f[1:].decode('ascii', 'replace') for f in body.split(b'\0') if f[:1] == b'C'), '?')
                        print(f'{label}: PostgreSQL error response, SQLSTATE={code}', flush=True)
                    else:
                        print(f'{label}: PostgreSQL response type={kind!r}, bytes={length}', flush=True)
        except (OSError, EOFError) as error:
            print(f'{label}: {type(error).__name__}: {error}', flush=True)
print('PG environment variable names:', ', '.join(sorted(k for k in os.environ if k.startswith('PG'))) or '(none)')
PY
