#!/usr/bin/env python3
"""Daily operational report; never invokes a model or sends an order."""
import argparse
import collections
import datetime as dt
import json
import os
import pathlib
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
MOSCOW = dt.timezone(dt.timedelta(hours=3))


def command(args):
    try:
        result = subprocess.run(args, cwd=ROOT, text=True, capture_output=True, timeout=90)
        if result.returncode:
            return None, (result.stderr.strip() or result.stdout.strip() or "command failed")[-1500:]
        return result.stdout.strip(), None
    except (OSError, subprocess.TimeoutExpired) as error:
        return None, str(error)


def generate():
    now = dt.datetime.now(MOSCOW)
    lines = ["AI Trader — Sandbox", "Отчёт на " + now.isoformat(timespec="seconds")]
    problems = []
    settings = {}
    env_file = ROOT / ".env"
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            key, separator, value = line.partition("=")
            if separator and key.strip() in ("AUTOMATION_EXECUTE_ORDERS", "AUTOMATION_START_MSK", "AUTOMATION_END_MSK", "AUTOMATION_ANALYSIS_TIMES_MSK"):
                settings[key.strip()] = value.strip().strip("\"'")
    auto = os.environ.get("AUTOMATION_EXECUTE_ORDERS", settings.get("AUTOMATION_EXECUTE_ORDERS", "false"))
    lines.append("Автоисполнение (конфигурация): " + auto)
    work_start = os.environ.get("AUTOMATION_START_MSK", settings.get("AUTOMATION_START_MSK", ""))
    work_end = os.environ.get("AUTOMATION_END_MSK", settings.get("AUTOMATION_END_MSK", ""))
    analysis_times = os.environ.get("AUTOMATION_ANALYSIS_TIMES_MSK", settings.get("AUTOMATION_ANALYSIS_TIMES_MSK", ""))
    if work_start and work_end:
        lines.append("Рабочие часы МСК: " + work_start + "–" + work_end + "; начала окон анализа: " + analysis_times)
    if auto.lower() not in ("true", "1", "t"):
        problems.append("Автоисполнение выключено в конфигурации")

    def trader(*args):
        output, error = command(["bash", "scripts/trader.sh", *args])
        if error:
            problems.append(" ".join(args) + ": " + error)
            return None
        try:
            return json.loads(output)
        except (TypeError, ValueError):
            problems.append(" ".join(args) + ": invalid JSON")
            return None

    service, error = command(["systemctl", "--user", "show", "ai-trader.service", "-p", "ActiveState", "-p", "SubState", "-p", "NRestarts", "-p", "ExecMainStatus"])
    lines += ["", "СЛУЖБА", service or error or "unknown"]
    if error or "ActiveState=active" not in (service or ""):
        problems.append("Фоновая служба не активна или недоступна")
    for timer in ["ai-trader-backup.timer", "ai-trader-report.timer"]:
        output, error = command(["systemctl", "--user", "is-active", timer])
        lines.append(timer + ": " + (output or error or "unknown"))
        if error or output != "active":
            problems.append(timer + " не активен")

    # Sample and reconcile with the actual broker before building the report.
    equity = trader("equity", "sample")
    control = trader("trading", "status")
    if control and control.get("paused"):
        problems.append("Торговля на паузе: " + control.get("reason", ""))
    lines += ["", "УПРАВЛЕНИЕ СЧЁТОМ", json.dumps(control, ensure_ascii=False)]
    if equity:
        lines += ["", "СВЕЖИЙ ЗАМЕР И СВЕРКА С БРОКЕРОМ", json.dumps(equity, ensure_ascii=False)]

    report = trader("report", "--days", "1")
    if report:
        if not report.get("journal_complete") or report.get("issues"):
            problems.append("Отчёт содержит проблемы учёта: " + json.dumps(report.get("issues"), ensure_ascii=False))
        lines += ["", "РЕЗУЛЬТАТ ЗА МОСКОВСКИЙ ДЕНЬ", json.dumps(report, ensure_ascii=False, indent=2)]
    history = trader("history", "--limit", "100")
    if history is not None:
        today = [row for row in history if dt.datetime.fromisoformat(row["created_at"]).astimezone(MOSCOW).date() == now.date()]
        counts = collections.Counter(row["status"] for row in today)
        lines += ["", "АНАЛИЗЫ СЕГОДНЯ (из последних 100): " + json.dumps(dict(counts), ensure_ascii=False)]
        for row in sorted(today, key=lambda item: item["created_at"]):
            lines.append(json.dumps({key: row.get(key) for key in ["id", "created_at", "provider", "automatic_execution", "status", "decision", "economy", "sandbox_order_state", "error"]}, ensure_ascii=False))
            if row["status"] in ("broker_error", "model_error"):
                problems.append("Ошибка анализа " + row["id"] + ": " + (row.get("error") or row["status"]))
        if not any(row.get("automatic_execution") for row in today):
            lines.append("Сегодня ещё нет анализов с включённым автоисполнением: проверь время запуска и торговые окна.")
    orders = trader("orders", "list")
    if orders is not None:
        lines += ["", "ЗАЯВКИ СЕГОДНЯ И НЕЗАВЕРШЁННЫЕ"]
        for row in reversed(orders):
            pending = row["state"] in ("prepared", "submitting", "acknowledged", "unknown", "active", "partial")
            if pending or dt.datetime.fromisoformat(row["created_at"]).astimezone(MOSCOW).date() == now.date():
                lines.append(json.dumps({key: row.get(key) for key in ["id", "analysis_id", "created_at", "state", "decision", "executed_lots", "cash_delta_nano", "commission_nano", "error"]}, ensure_ascii=False))
            if pending:
                problems.append("Незавершённая заявка " + row["id"] + ": " + row["state"])
    usage = trader("usage", "--days", "1")
    lines += ["", "РАСХОД ИИ СЕГОДНЯ", json.dumps(usage, ensure_ascii=False, indent=2)]

    journal, error = command(["journalctl", "--user", "-u", "ai-trader.service", "--since", now.strftime("%Y-%m-%d 00:00:00"), "--no-pager", "-o", "short-iso", "-n", "2000"])
    log_lines = (journal or "").splitlines()
    matches = [i for i, line in enumerate(log_lines) if any(word in line for word in ["cycle_error", "Error:", "failed", "Warning:"])]
    context = sorted({i for match in matches[-10:] for i in range(max(0, match-4), min(len(log_lines), match+5))})
    errors = [log_lines[i] for i in context]
    if errors:
        problems.append("В журнале службы есть ошибки/предупреждения; см. ниже")
    lines += ["", "ОШИБКИ СЛУЖБЫ СЕГОДНЯ (с контекстом)", "\n".join(errors) or error or "Не обнаружены в последних 2000 строках журнала."]
    backups = sorted((ROOT / ".backups").glob("ai-trader-*.dump"))
    lines += ["", "ПОСЛЕДНЯЯ РЕЗЕРВНАЯ КОПИЯ", backups[-1].name if backups else "Нет локальных копий"]
    if not backups or now.timestamp() - backups[-1].stat().st_mtime > 48 * 3600:
        problems.append("Нет свежей локальной резервной копии за последние 48 часов")
    lines += ["", "ТРЕБУЕТ ВНИМАНИЯ", "\n".join("- " + item for item in problems) if problems else "Автоматические проверки не обнаружили проблем.", "", "HOLD и отсутствие сделок допустимы. Убыток сам по себе не означает сбой."]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--save", action="store_true", help="save today's report and latest.txt locally")
    args = parser.parse_args()
    os.umask(0o077)
    report = generate()
    if args.save:
        directory = ROOT / ".reports"
        directory.mkdir(exist_ok=True)
        for name in [dt.datetime.now(MOSCOW).strftime("%Y-%m-%d.txt"), "latest.txt"]:
            with tempfile.NamedTemporaryFile(mode="w", dir=directory, delete=False) as output:
                output.write(report)
                temporary = output.name
            os.replace(temporary, directory / name)
        print("Daily report saved: " + str(directory / "latest.txt"))
    else:
        print(report, end="")


if __name__ == "__main__":
    main()
