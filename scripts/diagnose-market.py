#!/usr/bin/env python3
"""Read-only market API probes. Prints argument errors, never the credential."""
import datetime as dt
import json
import os
from pathlib import Path
import ssl
import urllib.error
import urllib.request

root = Path(__file__).resolve().parent.parent
values = {}
for line in (root / '.env').read_text().splitlines():
    line = line.strip()
    if not line or line.startswith('#') or '=' not in line:
        continue
    key, value = line.split('=', 1)
    values[key.strip()] = value.strip().strip('"').strip("'")
token = os.environ.get('TINVEST_TOKEN', values.get('TINVEST_TOKEN', ''))
if not token:
    raise SystemExit('TINVEST_TOKEN is missing')
bundle = root / 'bin' / 'ca-certificates.crt'
context = ssl.create_default_context(cafile=str(bundle) if bundle.is_file() else None)
now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
stamp = lambda when: when.strftime('%Y-%m-%dT%H:%M:%SZ')
day = now.replace(hour=0, minute=0, second=0)
probes = [
    ('MarketDataService/GetCandles', {
        'instrumentId': 'e6123145-9665-43e0-8413-cd61b8aa9b13',
        'from': stamp(now-dt.timedelta(days=7)), 'to': stamp(now),
        'interval': 'CANDLE_INTERVAL_HOUR', 'candleSourceType': 'CANDLE_SOURCE_EXCHANGE',
    }),
    ('InstrumentsService/TradingSchedules', {
        'exchange': '', 'from': stamp(day), 'to': stamp(day+dt.timedelta(days=2)),
    }),
]
for method, body in probes:
    print(method, json.dumps(body), flush=True)
    url = 'https://sandbox-invest-public-api.tbank.ru/rest/tinkoff.public.invest.api.contract.v1.' + method
    request = urllib.request.Request(url, data=json.dumps(body).encode(), method='POST', headers={
        'Authorization': 'Bearer '+token, 'Content-Type': 'application/json',
    })
    try:
        with urllib.request.urlopen(request, context=context, timeout=20) as response:
            data = json.load(response)
            if 'candles' in data:
                print('OK, candles:', len(data['candles']))
            else:
                print('OK, exchanges:', [e.get('exchange') for e in data.get('exchanges', [])])
    except urllib.error.HTTPError as error:
        raw = error.read(65536).decode(errors='replace').replace(token, '[redacted]')
        try:
            data = json.loads(raw)
            print('HTTP', error.code, 'code:', data.get('code'), 'message:', str(data.get('message', ''))[:2000])
        except ValueError:
            print('HTTP', error.code, '(non-JSON error body omitted)')
    except Exception as error:
        print(type(error).__name__, str(error).replace(token, '[redacted]')[:1000])
