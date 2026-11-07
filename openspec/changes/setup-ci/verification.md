# Проверка CI

Дата: 07.11.2026. Задача: [PRD-31](https://linear.app/po-sostavu/issue/PRD-31), часть CI.

## Локальные проверки

| Проверка                                                                           | Результат                                                                                                                      |
| ---------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `actionlint` 1.7.12, официальный архив с проверкой SHA-256                         | Workflow валиден, ошибок нет                                                                                                   |
| Итоговый shell-шаг `CI status`                                                     | Проверены все 64 сочетания `success`, `failure`, `cancelled`, `skipped` для трёх зависимостей. Успех только при трёх `success` |
| PWA: `npm ci`, `npm run check`, `npm run build`                                    | PASS. Node 24.19.0, npm 11.6.2, 28 тестов. Формат, ESLint, FSD и типы прошли                                                   |
| `npm audit --omit=dev --audit-level=low`                                           | PASS, 0 уязвимостей                                                                                                            |
| `npm audit --audit-level=moderate`                                                 | PASS. 4 low в цепочке dev-зависимости `deepmerge`, находки остаются видимыми                                                   |
| API: `task api:fmt:check`, `task api:lint`, `task api:test:race`, `task api:build` | PASS в Linux-контейнере `golang:1.27.3-bookworm`, GCC 12.2.0, Task 3.54.0. Линтер — 0 issues                                   |
| API: `task api:audit`                                                              | PASS на Windows: No vulnerabilities found                                                                                      |
| Docker PWA / API / migrate                                                         | Все три `docker build --pull` прошли с контекстами и target из workflow                                                        |
| Документация                                                                       | Проверены локальные ссылки, точечный diff, UTF-16 LE BOM корневого README сохранён                                             |
| Независимое ревью                                                                  | Существенных ошибок в событиях, командах, матрице, итоговом статусе и правах не выявлено                                       |
| OpenSpec                                                                           | `openspec validate --all --strict`: 3 изменения прошли                                                                         |

Workflow, новый регламент, OpenSpec и README приложений проверены Prettier. Старые `architecture.md`, `git-flow.md` и `configuration-and-releases.md` имели расхождения формата до изменения. Их таблицы не переформатированы целиком. `git diff --check` проходит.

При локальной переустановке зависимостей PWA запущенный Vite удерживал нативный Windows-модуль. Он был перезапущен после успешного `npm ci` на прежнем `127.0.0.1:5173`. Зависимости и lock-файл не менялись.

## Границы проверки

Локальные проверки не заменяют GitHub Actions: запуск `setup-*`, кэшей и Linux-аудита целиком подтверждён удалённым прогоном ниже. Локальный Linux-harness получил CR в последней команде аудита. Поэтому локальный аудит выполнен отдельно на Windows. На GitHub команда аудита прошла на Ubuntu.

Контейнерные проверки подтверждают сборку, но не сканируют ОС и Caddy и не разворачивают сервисы. Ограничения базового образа PWA остаются в [предыдущем аудите](../bootstrap-pwa/security-audit.md). Продуктовые функции, PostgreSQL-инварианты, интеграция PWA/API и деплой этим CI не проверяются. Правила доступа GitHub не меняются. PRD-31 целиком не закрывается.

## GitHub Actions

Первый реальный [прогон CI](https://github.com/po-sostavu/app/actions/runs/42389079028) для [PR #9](https://github.com/po-sostavu/app/pull/9) завершился успешно на коммите `be45c7a87d93ed9ab7f247e496ce448bcac4da59`.

Все шесть заданий успешны: `PWA checks`, `API checks`, `Docker (pwa)`, `Docker (api)`, `Docker (migrate)`, `CI status`. Это подтверждает установку закреплённых инструментов, исполнение тестов с race detector и govulncheck на `ubuntu-24.04`, npm-аудит и сборку всех образов на GitHub.

Этот отчёт фиксирует конкретный выполненный прогон. После его обновления GitHub повторно проверяет новый коммит. Перед слиянием требуется успешный `CI status` актуального HEAD, а не только ссылка на предыдущий результат. Действующие правила доступа не изменялись.
