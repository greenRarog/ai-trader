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
- Ручные лимитные Sandbox-заявки, журнал исполнения и восстановление без повторной отправки.
- Два дневных окна автоматического **анализа**, компактный контекст, кэш и лимит вызовов ИИ.
- Отчёты по капиталу, записанным денежным потокам, комиссиям, FIFO и токенам.
- Опциональные Telegram-уведомления, установщик пользовательских служб systemd.
- Резервные копии PostgreSQL с проверкой восстановления в изолированной БД.

## Что предстоит

- Подтвердить полный цикл BUY/SELL, комиссии и сбои исполнения на работающем Sandbox.
- Подключить автоматическое исполнение после испытаний; сейчас оно вызывается вручную.
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

Фоновый анализ и ежедневная проверяемая резервная копия:

```bash
python3 scripts/install-services.py
systemctl --user status ai-trader.service ai-trader-backup.timer
```

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
