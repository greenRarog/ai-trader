#!/usr/bin/env python3
"""Install user units, or render them inside the project with --render-only."""
import argparse,os,pathlib,shutil,subprocess
p=argparse.ArgumentParser()
p.add_argument('--render-only',action='store_true',help='write units under .state/systemd without installing or starting')
a=p.parse_args()
root=pathlib.Path(__file__).resolve().parent.parent
if any(c in str(root) for c in '\n\r'):p.error('Project path cannot contain line breaks.')
if os.geteuid()==0 and not a.render_only:p.error('Run as the same regular user who ran codex login, without sudo.')
for command in ['bash','docker']:
    if not shutil.which(command):p.error(f'{command} not found')
if not a.render_only and not (root/'bin/ai-trader').is_file():p.error('Build first: bash scripts/build-local.sh')
def q(value):return '"'+str(value).replace('\\','\\\\').replace('"','\\"').replace('%','%%')+'"'
bash=q(shutil.which('bash'));docker=q(shutil.which('docker'));project=q(root)
common=f'WorkingDirectory={str(root).replace(chr(37),chr(37)*2)}\nEnvironmentFile={str(root/".env").replace(chr(37),chr(37)*2)}\nEnvironment={q("PATH="+os.environ.get("PATH","/usr/local/bin:/usr/bin:/bin"))}\nUMask=0077\n'
pre=f'ExecStartPre={docker} compose --project-directory {project} up -d --wait postgres\n'
units={
'ai-trader.service':f'''[Unit]
Description=AI Trader Sandbox trading and account monitoring
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
{common}{pre}ExecStart={bash} {q(root/'scripts/trader.sh')} automation run
Restart=on-failure
RestartSec=30
TimeoutStartSec=180
TimeoutStopSec=45
KillMode=control-group

[Install]
WantedBy=default.target
''',
'ai-trader-backup.service':f'''[Unit]
Description=AI Trader PostgreSQL backup and isolated restore verification

[Service]
Type=oneshot
{common}{pre}ExecStart={bash} {q(root/'scripts/backup-db.sh')} --verify
TimeoutStartSec=900
Nice=10
''',
'ai-trader-backup.timer':'''[Unit]
Description=Daily AI Trader verified backup

[Timer]
OnCalendar=*-*-* 03:15:00 Europe/Moscow
RandomizedDelaySec=10m
Persistent=true
Unit=ai-trader-backup.service

[Install]
WantedBy=timers.target
''',
'ai-trader-report.service':f'''[Unit]
Description=AI Trader daily operational report

[Service]
Type=oneshot
{common}{pre}ExecStart={bash} {q(root/'scripts/daily-report.sh')} --save
TimeoutStartSec=900
''',
'ai-trader-report.timer':'''[Unit]
Description=Evening AI Trader operational report

[Timer]
OnCalendar=*-*-* 21:15:00 Europe/Moscow
Persistent=true
Unit=ai-trader-report.service

[Install]
WantedBy=timers.target
'''}
destination=root/'.state/systemd' if a.render_only else pathlib.Path(os.environ.get('XDG_CONFIG_HOME',str(pathlib.Path.home()/'.config')))/'systemd/user'
destination.mkdir(parents=True,exist_ok=True)
for name,text in units.items():(destination/name).write_text(text)
print(f'Units written: {destination}')
if not a.render_only:
    subprocess.run(['systemctl','--user','daemon-reload'],check=True)
    subprocess.run(['systemctl','--user','enable','--now','ai-trader.service','ai-trader-backup.timer','ai-trader-report.timer'],check=True)
    print('Trader, backup timer and evening report timer enabled. Trading pause state was not changed.')
    print('For boot without an interactive login: loginctl enable-linger "$USER" (may require administrator rights).')
