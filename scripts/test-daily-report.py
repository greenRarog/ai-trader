#!/usr/bin/env python3
"""Offline checks of daily reporting, including visible failure states."""
import contextlib
import datetime as dt
import importlib.util
import io
import json
import os
import pathlib
import stat
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("daily_report", pathlib.Path(__file__).with_name("daily-report.py"))
report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(report)


class DailyReportTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = pathlib.Path(self.directory.name)
        (self.root / ".backups").mkdir()
        (self.root / ".backups/ai-trader-20261026T060000000000000Z.dump").write_bytes(b"test")
        self.root_patch = patch.object(report, "ROOT", self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.env_patch = patch.dict(os.environ, {"AUTOMATION_EXECUTE_ORDERS": "true"})
        self.env_patch.start()
        self.addCleanup(self.env_patch.stop)
        self.paused = False
        self.failed_equity = False
        self.calls = []

    def command(self, args):
        self.calls.append(args)
        if args[0] == "systemctl":
            return ("ActiveState=active\nSubState=running\nNRestarts=0\nExecMainStatus=0" if "show" in args else "active"), None
        if args[0] == "journalctl":
            return "", None
        key = tuple(args[2:])
        if key == ("equity", "sample") and self.failed_equity:
            return None, "cash mismatch"
        data = {
            ("equity", "sample"): {"current_nano": "99999708220000"},
            ("trading", "status"): {"paused": self.paused, "reason": "ledger mismatch" if self.paused else ""},
            ("report", "--days", "1"): {"journal_complete": True, "issues": [], "confirmed_commissions_rub": "0.29178"},
            ("history", "--limit", "100"): [{"id": "test", "created_at": dt.datetime.now(dt.timezone.utc).isoformat(), "status": "validated_proposal", "automatic_execution": True, "decision": {"action": "HOLD"}}],
            ("orders", "list"): [],
            ("usage", "--days", "1"): {"days": []},
        }
        return json.dumps(data[key]), None

    def test_healthy_hold_is_reported_without_trading_or_model_calls(self):
        with patch.object(report, "command", self.command):
            text = report.generate()
        self.assertIn("HOLD", text)
        self.assertIn("0.29178", text)
        self.assertIn("Автоматические проверки не обнаружили проблем", text)
        self.assertFalse(any("analyze" in call or "execute" in call for call in self.calls))

    def test_broker_failure_and_pause_remain_visible(self):
        self.paused = self.failed_equity = True
        with patch.object(report, "command", self.command):
            text = report.generate()
        self.assertIn("cash mismatch", text)
        self.assertIn("Торговля на паузе", text)
        self.assertIn("РАСХОД ИИ СЕГОДНЯ", text)
        self.assertNotIn("Автоматические проверки не обнаружили проблем", text)

    def test_save_produces_private_latest_and_dated_reports(self):
        with patch.object(report, "command", self.command), patch("sys.argv", ["daily-report.py", "--save"]), contextlib.redirect_stdout(io.StringIO()):
            report.main()
        latest = self.root / ".reports/latest.txt"
        self.assertEqual(stat.S_IMODE(latest.stat().st_mode), 0o600)
        dated = self.root / ".reports" / (dt.datetime.now(report.MOSCOW).strftime("%Y-%m-%d") + ".txt")
        self.assertEqual(latest.read_text(), dated.read_text())


if __name__ == "__main__":
    unittest.main()
