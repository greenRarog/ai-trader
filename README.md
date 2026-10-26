# AI Trader

Исследовательский торговый агент для российского рынка: **LLM предлагает
BUY / SELL / HOLD**, Go проверяет ограничения и сохраняет решения. Сейчас
используется только **T-Invest Sandbox с виртуальными средствами**.

Go · PostgreSQL · Docker Compose · CLI · Codex через ChatGPT или OpenAI API.
Начальный виртуальный капитал — 100 000 ₽. Веб-интерфейса нет.

## Что реализовано

- Sandbox-счёт, баланс, котировки, свечи, стакан и торговое расписание.
- Структурированные решения ИИ и полная история в PostgreSQL.
- Risk Engine: ограничения денег, позиций, дневных потерь и просадки; постоянная пауза.
- Ручные и автоматические лимитные Sandbox-заявки, журнал и восстановление без повторной отправки.
- Два дневных окна автоматического **анализа**, компактный контекст, кэш и лимит вызовов ИИ.
- Отчёты по капиталу, записанным денежным потокам, комиссиям, FIFO и токенам.
- Опциональные Telegram-уведомления, установщик пользовательских служб systemd.
- Резервные копии PostgreSQL с проверкой восстановления в изолированной БД.

## Что предстоит

- Расширить Sandbox-проверки частичных исполнений и сетевых сбоев; минимальный
  BUY/SELL, комиссии и повтор команд проверены на работающем Sandbox.
- Накопить статистику автономной работы и проверить поведение в длительном запуске.
- Длительный эксперимент, пассивные ориентиры, модельные налоги и виртуальная фиксация прибыли.
- Расширенный импорт внешних операций и корпоративных событий.
- Реальный торговый контур: не реализован и требует отдельного решения владельца.

## Развернуть у себя

Нужны Linux, Git, Python 3, Docker Engine с Compose и доступ пользователя к Docker.
Для текущего провайдера нужен Codex CLI на хосте с выполненным `codex login`.
Устанавливать Go на хост не требуется.

```bash
git clone git@github.com:GreenRarog/ai-trader.git
cd ai-trader
cp .env.example .env
chmod 600 .env
```

В `.env` задайте `TINVEST_TOKEN` от Sandbox и собственный `POSTGRES_PASSWORD`.
По умолчанию `AI_PROVIDER=codex_cli`; для OpenAI API нужны отдельные настройки.
Секреты остаются локально.

```bash
docker compose up -d --wait postgres
bash scripts/build-local.sh
bash scripts/trader.sh db migrate
bash scripts/trader.sh accounts
```

Для **нового пустого** Sandbox-счёта:

```bash
bash scripts/trader.sh account create --name ai-trader
```

Запишите полученный ID в `TINVEST_ACCOUNT_ID` в `.env`. Затем один раз пополните:

```bash
bash scripts/trader.sh account fund --amount 100000 --confirm
bash scripts/trader.sh trading resume
```

Если счёт уже существует и пополнен, используйте его ID и пропустите создание
и пополнение. Каждое новое пополнение **добавляет** деньги к остатку.

```bash
bash scripts/trader.sh market --compact
bash scripts/trader.sh automation tick
bash scripts/trader.sh report --days 30
bash scripts/trader.sh usage --days 7
```

Для автономного Sandbox-исполнения добавьте в `.env`:

```dotenv
AUTOMATION_EXECUTE_ORDERS=true
```

Фоновый трейдер, ежедневная проверяемая резервная копия и отчёт:

```bash
python3 scripts/install-services.py
systemctl --user status ai-trader.service ai-trader-backup.timer
```

Время задач задаётся через `DAILY_REPORT_TIME_MSK` и `BACKUP_TIME_MSK`.
В текущем сокращённом режиме трейдер работает 09:30–15:00 МСК, отчёт — в 15:00.
Сводка сохраняется в `.reports/latest.txt`; утренний запуск — `bash scripts/start-day.sh`.
Проверка работы и чтение результатов: [ежедневный контроль](docs/DAILY_CHECK.md).

Для работы после перезагрузки без входа в сеанс нужен user lingering:
`loginctl enable-linger "$USER"`. Постоянная пауза: `bash scripts/trader.sh trading pause`.

Проверки и разовая резервная копия:

```bash
docker compose --profile test run --rm --build tests
bash scripts/backup-db.sh --verify
```

Подробности: [эксплуатация и ограничения](docs/OPERATIONS.md),
[службы, Telegram, аналитика и восстановление](docs/DEPLOYMENT.md),
[контекст проекта](PROJECT_CONTEXT.md).
