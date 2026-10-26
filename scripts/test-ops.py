#!/usr/bin/env python3
"""Offline operation-script tests. Docker calls use a fake executable only."""
import hashlib,json,os,pathlib,stat,subprocess,sys,tempfile,unittest
ROOT=pathlib.Path(__file__).resolve().parent.parent
class OperationTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='ai-trader-ops-');self.addCleanup(self.temp.cleanup)
        self.root=pathlib.Path(self.temp.name);self.backups=self.root/'backups';self.log=self.root/'docker.log'
        docker=self.root/'docker'
        docker.write_text('#!'+sys.executable+'''\nimport os,sys,pathlib,json
args=' '.join(sys.argv[1:])
with open(os.environ['MOCK_DOCKER_LOG'],'a') as log: log.write(json.dumps(sys.argv[1:])+'\\n')
if 'pg_dump' in args:
 if os.environ.get('MOCK_DUMP_FAIL')=='1': sys.exit(1)
 sys.stdout.buffer.write(b'PGDMP offline test archive')
elif 'pg_restore' in args:
 if not sys.stdin.buffer.read().startswith(b'PGDMP'):sys.exit(2)
elif 'psql' in args:
 sql=sys.stdin.read()
 if 'isolated restore check' not in sql:sys.exit(3)
''')
        docker.chmod(0o700)
        self.env=os.environ.copy();self.env.update(PATH=str(self.root)+os.pathsep+self.env['PATH'],BACKUP_DIR=str(self.backups),BACKUP_KEEP='2',MOCK_DOCKER_LOG=str(self.log),DATABASE_URL='')
    def run_script(self,name,*args,ok=True):
        result=subprocess.run(['bash',str(ROOT/'scripts'/name),*args],env=self.env,text=True,capture_output=True)
        if ok:self.assertEqual(result.returncode,0,result.stderr)
        else:self.assertNotEqual(result.returncode,0)
        return result
    def test_backup_and_isolated_restore(self):
        self.run_script('backup-db.sh','--verify')
        dump=next(self.backups.glob('*.dump'))
        self.assertEqual(stat.S_IMODE(dump.stat().st_mode),0o600)
        self.assertEqual(dump.with_name(dump.name+'.sha256').read_text().split()[0],hashlib.sha256(dump.read_bytes()).hexdigest())
        calls=self.log.read_text();self.assertIn('postgres-restorecheck',calls)
        for line in calls.splitlines():
            call=" ".join(json.loads(line))
            if 'pg_restore' in call and '--list' not in call:self.assertIn('postgres-restorecheck',call)
        previous=self.log.read_text();dump.write_bytes(b'tampered')
        self.run_script('check-backup.sh',str(dump),ok=False)
        self.assertEqual(self.log.read_text(),previous,'bad checksum reached Docker')
    def test_failed_dump_never_publishes_partial_archive(self):
        self.env['MOCK_DUMP_FAIL']='1';self.run_script('backup-db.sh',ok=False)
        self.assertFalse(list(self.backups.glob('*.dump')))
        self.assertFalse([p for p in self.backups.glob('.backup.*') if p.name!='.backup.lock'])
    def test_retention_only_removes_our_old_timestamped_archives(self):
        self.backups.mkdir()
        for name in ['ai-trader-20200101T000000000000000Z.dump','ai-trader-20200102T000000000000000Z.dump']:
            (self.backups/name).write_bytes(b'old');(self.backups/(name+'.sha256')).write_text('old')
        other=self.backups/'manual.dump';other.write_bytes(b'keep')
        self.run_script('backup-db.sh');self.assertFalse((self.backups/'ai-trader-20200101T000000000000000Z.dump').exists())
        self.assertTrue((self.backups/'ai-trader-20200102T000000000000000Z.dump').exists());self.assertTrue(other.exists())
    def test_external_database_is_not_silently_backed_up_as_local(self):
        self.env['DATABASE_URL']='postgres://external.example/research'
        self.run_script('backup-db.sh',ok=False)
        self.assertFalse(self.log.exists())
    def test_rendered_units_have_no_embedded_credentials(self):
        subprocess.run([sys.executable,str(ROOT/'scripts/install-services.py'),'--render-only'],check=True,capture_output=True)
        service=(ROOT/'.state/systemd/ai-trader.service').read_text()
        self.assertIn(' automation run',service);self.assertIn('EnvironmentFile='+str(ROOT/'.env'),service)
        self.assertNotIn('POSTGRES_PASSWORD=',service);self.assertNotIn('TELEGRAM_BOT_TOKEN=',service)
    def test_custom_report_and_backup_timers(self):
        settings=self.env.copy();settings.update(DAILY_REPORT_TIME_MSK='15:00',BACKUP_TIME_MSK='14:50')
        subprocess.run([sys.executable,str(ROOT/'scripts/install-services.py'),'--render-only'],check=True,capture_output=True,env=settings)
        report=(ROOT/'.state/systemd/ai-trader-report.timer').read_text()
        backup=(ROOT/'.state/systemd/ai-trader-backup.timer').read_text()
        self.assertIn('OnCalendar=*-*-* 15:00:00 Europe/Moscow',report)
        self.assertIn('Persistent=false',report)
        self.assertIn('OnCalendar=*-*-* 14:50:00 Europe/Moscow',backup)
        self.assertIn('RandomizedDelaySec=0',backup)
        settings['DAILY_REPORT_TIME_MSK']='15:00\nExecStart=unexpected'
        result=subprocess.run([sys.executable,str(ROOT/'scripts/install-services.py'),'--render-only'],capture_output=True,env=settings)
        self.assertNotEqual(result.returncode,0)
if __name__=='__main__':unittest.main()
